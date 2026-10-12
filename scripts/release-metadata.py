#!/usr/bin/env python3
"""Emit target-specific CycloneDX SBOMs and unsigned SLSA provenance.

Read embedded Go build information, never shell environment snapshots or local
credential files. The source digest covers an explicit build-input allowlist.
"""
import argparse
import base64
import datetime
import hashlib
import json
from pathlib import Path
import re
import subprocess
from urllib.parse import quote


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def source_digest(root):
    files = [root / "go.mod", root / "go.sum", root / "Dockerfile"]
    for directory in ("cmd", "internal"):
        files.extend(p for p in (root / directory).rglob("*") if p.is_file())
    files = sorted(files, key=lambda p: p.relative_to(root).as_posix())
    value = hashlib.sha256()
    for path in files:
        value.update(path.relative_to(root).as_posix().encode())
        value.update(b"\0")
        value.update(digest(path).encode())
        value.update(b"\n")
    return value.hexdigest()


def generate(root, directory, version):
    binaries = sorted(p for p in directory.glob("twi_*") if re.fullmatch(r"twi_(linux|darwin)_(amd64|arm64)", p.name))
    if not binaries:
        raise ValueError("release directory contains no supported binary artifacts")
    timestamp = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
    revision = run("git", "-C", str(root), "rev-parse", "HEAD")
    dirty = bool(run("git", "-C", str(root), "status", "--porcelain", "--untracked-files=normal"))
    tree = source_digest(root)
    for binary in binaries:
        info = json.loads(run("go", "version", "-m", "-json", str(binary)))
        if info.get("Path") != "github.com/worxbend/twi/cmd/twi":
            raise ValueError(f"{binary.name}: artifact is not the twi CLI")
        settings = {s["Key"]: s["Value"] for s in info.get("Settings", [])}
        if settings.get("-trimpath") != "true":
            raise ValueError(f"{binary.name}: release artifact must use trimpath")
        if settings.get("CGO_ENABLED") != "0":
            raise ValueError(f"{binary.name}: release artifact must have CGO_ENABLED=0")
        embedded_version=re.search(r'(?:^|\s)-X\s+github\.com/worxbend/twi/internal/cli\.buildVersion=([^\s]+)',settings.get('-ldflags',''))
        # Go omits linker flags from build info under trimpath. The injected
        # public version still occurs in the executable's string data.
        if (embedded_version is not None and embedded_version.group(1)!=version) or version.encode() not in binary.read_bytes():
            raise ValueError(f'{binary.name}: metadata version differs from embedded release version')
        target = settings["GOOS"] + "/" + settings["GOARCH"]
        if binary.name != "twi_" + target.replace("/", "_"):
            raise ValueError(f"{binary.name}: embedded target does not match filename")
        components = [{"type":"library","name":"Go standard library","version":info["GoVersion"],"bom-ref":"go:stdlib:"+info["GoVersion"]}]
        dependencies = ["go:stdlib:"+info["GoVersion"]]
        for dep in info.get("Deps", []):
            module = dep.get("Replace") or dep
            path, dep_version = module["Path"], module["Version"]
            if not dep_version or dep_version == "(devel)":
                raise ValueError(f"{binary.name}: dependency has no immutable module version: {path}")
            ref = "pkg:golang/" + quote(path, safe="/") + "@" + quote(dep_version, safe="")
            component = {"type": "library", "name": path, "version": dep_version, "bom-ref": ref, "purl": ref}
            if module.get("Sum", "").startswith("h1:"):
                component["hashes"] = [{"alg": "SHA-256", "content": base64.b64decode(module["Sum"][3:]).hex()}]
            components.append(component)
            dependencies.append(ref)
        app_ref = "pkg:golang/github.com/worxbend/twi@" + quote(version, safe="")
        sbom = {"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
                "metadata": {"timestamp": timestamp, "component": {"type": "application", "name": "twi", "version": version, "bom-ref": app_ref, "licenses": [{"license": {"id": "MIT"}}]},
                             "properties": [{"name": "twi:target", "value": target}, {"name": "twi:go-version", "value": info["GoVersion"]}]},
                "components": sorted(components, key=lambda c: c["name"]),
                "dependencies": [{"ref": app_ref, "dependsOn": sorted(dependencies)}]}
        sbom_path = binary.with_name(binary.name + ".sbom.cdx.json")
        write_json(sbom_path, sbom)
        provenance = {"_type": "https://in-toto.io/Statement/v1", "subject": [
            {"name": binary.name, "digest": {"sha256": digest(binary)}},
            {"name": sbom_path.name, "digest": {"sha256": digest(sbom_path)}}],
            "predicateType": "https://slsa.dev/provenance/v1", "predicate": {
                "buildDefinition": {"buildType": "https://github.com/worxbend/twi/blob/main/scripts/release-dry-run.sh",
                    "externalParameters": {"version": version, "target": target},
                    "internalParameters": {"trimpath": True, "cgoEnabled": False, "goVersion": info["GoVersion"], "sourceDirty": dirty},
                    "resolvedDependencies": [{"uri": "git+https://github.com/worxbend/twi", "digest": {"gitCommit": revision}},
                                             {"uri": "twi:build-input-tree", "digest": {"sha256": tree}}]},
                "runDetails": {"builder": {"id": "https://github.com/worxbend/twi/blob/main/scripts/release-dry-run.sh"},
                               "metadata": {"finishedOn": timestamp}}}}
        write_json(binary.with_name(binary.name + ".provenance.json"), provenance)
    assets = sorted(p for binary in binaries for p in (binary, binary.with_name(binary.name+".sbom.cdx.json"), binary.with_name(binary.name+".provenance.json")))
    if (directory/"install.sh").is_file():
        assets.append(directory/"install.sh")
        assets.sort()
    for asset in assets:
        asset.with_name(asset.name + ".sha256").write_text(f"{digest(asset)}  {asset.name}\n")
    (directory / "SHA256SUMS").write_text("".join(f"{digest(asset)}  {asset.name}\n" for asset in assets))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    generate(Path(__file__).resolve().parents[1], args.directory.resolve(), args.version)


if __name__ == "__main__":
    main()

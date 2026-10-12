#!/usr/bin/env python3
"""Collect reproducible TUI workloads, CPU/heap profiles and numeric budgets.

Usage: python3 scripts/profile-tui.py [--output /tmp/twi-tui-profile] [--check]
Runs deterministic, non-network fixtures with two Go workers. No credentials,
saved configuration or terminal captures are read. Requires Go and rg on PATH.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import statistics
import subprocess
import tempfile
import time

# Rounded headroom from the 2026-10-12 Linux amd64/5700X baseline. Timing and CPU
# gates require comparable idle hardware; allocation and memory gates travel better.
BENCH_BUDGETS = {
    "quiet-one": {"ns/op": 5_000_000, "B/op": 1_500_000, "allocs/op": 45_000, "retained-B/channel": 96_000},
    "quiet-many": {"ns/op": 9_000_000, "B/op": 3_000_000, "allocs/op": 90_000, "retained-B/channel": 3_000},
    "busy-one": {"ns/op": 40_000_000, "B/op": 30_000_000, "allocs/op": 130_000, "retained-B/channel": 12_000_000},
    "busy-many": {"ns/op": 35_000_000, "B/op": 28_000_000, "allocs/op": 125_000, "retained-B/channel": 4_000_000},
    "busy-animated": {"ns/op": 45_000_000, "B/op": 40_000_000, "allocs/op": 170_000, "retained-B/channel": 4_000_000},
}
RUNTIME_BUDGETS = {
    "quiet-one": {"core_utilization_percent": 5, "view_p95_ms": 10, "allocated_bytes_per_second": 3_000_000},
    "quiet-many": {"core_utilization_percent": 20, "view_p95_ms": 15, "allocated_bytes_per_second": 35_000_000},
    "busy-many": {"core_utilization_percent": 150, "view_p95_ms": 25, "allocated_bytes_per_second": 850_000_000, "arrival_latency_p95_ms": 200, "minimum_processed_fraction": 0.80, "maximum_dropped_fraction": 0.01},
    "busy-animated": {"core_utilization_percent": 180, "view_p95_ms": 25, "allocated_bytes_per_second": 1_200_000_000, "arrival_latency_p95_ms": 200, "minimum_processed_fraction": 0.80, "maximum_dropped_fraction": 0.01},
    "busy-many-slow-terminal": {"core_utilization_percent": 100, "view_p95_ms": 25, "allocated_bytes_per_second": 600_000_000, "arrival_latency_p95_ms": 3000, "minimum_processed_fraction": 0.35, "maximum_dropped_fraction": 0.25},
}


def run(command, output, environment=None):
    with output.open("w") as log:
        completed = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, env=environment)
    if completed.returncode:
        raise SystemExit(f"Command failed ({completed.returncode}); inspect {output}")


def source_digest():
    names = subprocess.check_output(["rg", "--files", "-g", "*.go", "-g", "go.mod", "-g", "go.sum"], text=True).splitlines()
    digest = hashlib.sha256()
    for name in sorted(names):
        digest.update(name.encode()+b"\0")
        digest.update(Path(name).read_bytes())
    return digest.hexdigest()


def parse_benchmarks(path):
    samples = {}
    for line in path.read_text().splitlines():
        match = re.match(r"BenchmarkTUI(Workload|RetainedMemory)/([^\s]+)-\d+\s+\d+\s+(.+)", line)
        if not match:
            continue
        workload = match.group(2)
        values = samples.setdefault(workload, {})
        for number, unit in re.findall(r"([\d.]+)\s+(\S+)", match.group(3)):
            # Memory loop timings are a keep-alive shell, not a performance metric.
            if match.group(1) == "RetainedMemory" and unit in ("ns/op", "B/op", "allocs/op"):
                continue
            values.setdefault(unit, []).append(float(number))
    return {name: {unit: {"median": statistics.median(numbers), "minimum": min(numbers), "maximum": max(numbers), "samples": len(numbers)} for unit, numbers in metrics.items()} for name, metrics in samples.items()}


def budget_failures(benchmarks, runtime):
    failures = []
    for name, budgets in BENCH_BUDGETS.items():
        for unit, limit in budgets.items():
            observed = benchmarks.get(name, {}).get(unit, {}).get("median")
            if observed is None or observed > limit:
                failures.append(f"{name}: {unit} {observed} exceeds {limit}")
    actual = {row["workload"]: row for row in runtime}
    for name, budgets in RUNTIME_BUDGETS.items():
        if name not in actual:
            failures.append(f"{name}: runtime sample missing")
            continue
        row = actual[name]
        for metric, limit in budgets.items():
            if metric.startswith("minimum_") or metric.startswith("maximum_"):
                continue
            if row[metric] > limit:
                failures.append(f"{name}: {metric} {row[metric]:.3f} exceeds {limit}")
        if row["input_queue_peak"] > row["input_queue_capacity"] or row["input_queue_capacity"] != 128:
            failures.append(f"{name}: input queue bound changed")
        if row["offered_messages"]:
            processed = row["processed_messages"]/row["offered_messages"]
            dropped = row["dropped_messages"]/row["offered_messages"]
            if processed < budgets.get("minimum_processed_fraction", 0):
                failures.append(f"{name}: processed fraction {processed:.3f} below budget")
            if dropped > budgets.get("maximum_dropped_fraction", 1):
                failures.append(f"{name}: dropped fraction {dropped:.3f} exceeds budget")
    return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--check", action="store_true", help="enforce baseline budgets on comparable idle hardware")
    options = parser.parse_args()
    repository = Path(__file__).resolve().parent.parent
    os.chdir(repository)
    directory = options.output.resolve() if options.output else Path(tempfile.mkdtemp(prefix="twi-tui-performance-"))
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    environment = os.environ.copy()
    environment.update(GOMAXPROCS="2", GOGC="100", GOMEMLIMIT="off")
    environment.pop("GODEBUG", None)
    started = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    digest = source_digest()
    run(["go", "version"], directory/"go-version.txt", environment)
    executable = directory/"app.test"
    run(["go", "test", "-c", "-o", str(executable), "./internal/app"], directory/"build-log.txt", environment)
    print(f"Collecting TUI model benchmarks: {directory}", flush=True)
    run([str(executable), "-test.run=^TestQuietOffDoesNotScheduleRedraw$", "-test.bench=^BenchmarkTUIWorkload$", "-test.benchmem", "-test.benchtime=1s", "-test.count=3", "-test.cpu=2"], directory/"benchmarks.txt", environment)
    run([str(executable), "-test.run=^$", "-test.bench=^BenchmarkTUIRetainedMemory$", "-test.benchmem", "-test.benchtime=1x", "-test.count=3", "-test.cpu=2"], directory/"memory.txt", environment)
    print("Collecting real event-loop CPU/allocation profiles and terminal pressure", flush=True)
    environment.update(TWI_TUI_PROFILE_DIR=str(directory), TWI_TUI_PROFILE_DURATION="3s")
    cpu = directory/"runtime.cpu.pprof"
    allocations = directory/"runtime.allocs.pprof"
    run([str(executable), "-test.run=^TestTUIRuntimeProfile$", "-test.count=1", "-test.cpu=2", "-test.v", "-test.cpuprofile", str(cpu), "-test.memprofile", str(allocations), "-test.memprofilerate=16384"], directory/"runtime-log.txt", environment)
    run(["go", "tool", "pprof", "-top", "-nodecount=15", str(executable), str(cpu)], directory/"cpu-top.txt", environment)
    run(["go", "tool", "pprof", "-top", "-alloc_space", "-nodecount=15", str(executable), str(allocations)], directory/"alloc-top.txt", environment)
    run(["go", "tool", "pprof", "-tags", str(executable), str(cpu)], directory/"cpu-workloads.txt", environment)
    benchmarks = parse_benchmarks(directory/"benchmarks.txt")
    memory = parse_benchmarks(directory/"memory.txt")
    for name, metrics in memory.items():
        benchmarks.setdefault(name, {}).update(metrics)
    runtime = json.loads((directory/"runtime.json").read_text())
    failures = budget_failures(benchmarks, runtime)
    summary = {"started_at": started, "source_sha256": digest, "source_unchanged": source_digest() == digest, "go_version": (directory/"go-version.txt").read_text().strip(), "gomaxprocs": 2, "gogc": 100, "gomemlimit": "off", "viewport": "120x40", "benchmark_medians": benchmarks, "runtime": runtime, "benchmark_budgets": BENCH_BUDGETS, "runtime_budgets": RUNTIME_BUDGETS, "budget_failures": failures, "budget_hardware_note": "Timing, CPU and throughput comparisons require comparable idle hardware; no cross-hardware RPS guarantee."}
    (directory/"summary.json").write_text(json.dumps(summary, indent=2)+"\n")
    print(f"Artifacts: {directory}", flush=True)
    if failures:
        print("Budget observations:\n"+"\n".join(failures), flush=True)
    else:
        print("All reference budgets pass", flush=True)
    if not summary["source_unchanged"]:
        print("Source changed during collection; repeat for a stable comparison", flush=True)
    if options.check and (failures or not summary["source_unchanged"]):
        raise SystemExit(1)


if __name__ == "__main__":
    main()

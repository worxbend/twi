# Documentation

This directory is the product source of truth for `twi`. The README gives the fast path; these documents explain how to run, configure, debug, contribute, validate, and release the project without relying on tribal knowledge.

The [project website](https://worxbend.github.io/twi/) is a visual tour: screenshots, themes, and the fast install path. It is built from `site/` by `scripts/build-site.sh` and deployed by the Pages workflow.

## Start Here

| Audience | Read this | Purpose |
| --- | --- | --- |
| New user | [quickstart.md](quickstart.md) | Run mock chat, configure live Twitch chat, and learn the key bindings. |
| First live login | [register-twitch-app.md](register-twitch-app.md) | Register your own Twitch application, hand `twi` the client ID/secret, and learn the minimal config to start. |
| Operator | [config.md](config.md) and [auth.md](auth.md) | Understand config precedence, credential sources, login, token refresh, and redaction. |
| Docker user | [docker.md](docker.md) | Build and run the local container without baking secrets into the image. |
| Contributor | [../CONTRIBUTING.md](../CONTRIBUTING.md), [development.md](development.md), and [code-style.md](code-style.md) | Work safely in the codebase, run checks, and preserve package boundaries. |
| Security reviewer | [../SECURITY.md](../SECURITY.md) | Report credential-handling issues without exposing secrets. |
| Release owner | [release.md](release.md) and [manual-validation.md](manual-validation.md) | Run release packaging, record manual checks, and avoid unsupported claims. |
| Anyone upgrading | [../CHANGELOG.md](../CHANGELOG.md) | See what changed between releases, including behavior changes. |
| Product reviewer | [product/requirements.md](product/requirements.md), [product/backlog.md](product/backlog.md), and [product/risk-register.md](product/risk-register.md) | Review shipped behavior, planned scope, and known risks. |

## Current Support Boundary

`twi` targets Unix-like terminals and Docker. The release dry-run builds Linux and macOS binaries by default and rejects unsupported Windows targets. Saved credentials are Unix-only through the restrictive credential-file store. Non-Unix builds, if created manually, must use environment variables or a private flat config file and must keep saved credentials disabled.

## Architecture Records

The ADRs capture decisions that should not be re-litigated casually:

- [adr/0001-use-twitch-irc-for-mvp-chat-transport.md](adr/0001-use-twitch-irc-for-mvp-chat-transport.md)
- [adr/0002-wrap-helix-identity-and-asset-apis.md](adr/0002-wrap-helix-identity-and-asset-apis.md)
- [adr/0003-use-kitty-graphics-as-first-image-protocol.md](adr/0003-use-kitty-graphics-as-first-image-protocol.md) (superseded — Kitty graphics image rendering was removed)
- [adr/0004-normalize-chat-events-before-rendering.md](adr/0004-normalize-chat-events-before-rendering.md)
- [adr/0005-use-bounded-animation-scheduler.md](adr/0005-use-bounded-animation-scheduler.md)
- [adr/0006-pin-latest-stable-go-toolchain-and-module-tools.md](adr/0006-pin-latest-stable-go-toolchain-and-module-tools.md)

For the package-level map and runtime data flow, read [architecture.md](architecture.md).

## Quality Rules

The default quality gate is credential-free. It uses isolated config/cache directories and empty Twitch credential environment variables so checks do not depend on a developer's local account. Contributor-facing commands live in [../CONTRIBUTING.md](../CONTRIBUTING.md), and the deeper architecture/testing notes live in [development.md](development.md).

Docs must be honest about evidence. If a feature needs real Twitch credentials or Docker daemon access, record the check in [manual-validation.md](manual-validation.md) before making a support claim.

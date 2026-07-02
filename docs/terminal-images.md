# Terminal Images

`twi` supports fallback-safe inline image plumbing for avatars, Twitch emotes, and standard emoji in capable terminals. The Kitty-compatible renderer core exists behind the internal image renderer boundary, and chat rows can reserve image placeholders and substitute prepared image cells without changing fallback text. The current MVP implements ready text, Unicode, initials, compact badge, and emote-token fallbacks; live avatar, badge, emote, and standard emoji metadata can be resolved and cached, and visible-row asset events can prepare fixed-width image cells without blocking input.

## Current State

- Text, Unicode, initials, and compact badge fallbacks are implemented for chat rows.
- Renderer asset fallback fragments can reserve stable cell widths before images are available.
- Chat row generation can attach prepared renderer cells by stable URL-free asset key. Unsupported terminals, image-off mode, missing assets, and render failures keep the same fallback rows and reserved widths.
- `internal/render.PNGImagePreparer` decodes bounded downloaded PNG, JPEG, and first-frame GIF assets, crops/scales them to the requested terminal cell rectangle, and writes renderer-ready PNG files without exposing source URLs, source paths, or token-like values in errors.
- `internal/assets.PublicImageDownloader` fetches only validated public HTTP(S) PNG, JPEG, and GIF sources, validates redirects and public hosts, enforces response-size limits, writes URL-free local files without sending auth or cookie headers, and records a URL-free SHA-256 payload identity for the downloaded bytes.
- Visible-row asset events remember permanent preparation/render failures by URL-free asset identity, safe downloaded record metadata, optional payload identity, and requested cell size so unchanged corrupt, oversized, unsafe, or unsupported assets keep stable fallbacks without repeated decode/render work while changed bytes can retry.
- `internal/storage.AssetCache` provides context-aware cache methods. The in-memory implementation is intended for deterministic tests, and `internal/storage.DiskAssetCache` persists metadata plus cache-owned bytes under the platform cache directory using deterministic hashed paths.
- `twi doctor` reports image-related readiness through terminal color hints, Kitty/Ghostty environment signals, cache writability, selected image/avatar/emoji/emote modes, and the resolved image capability state.
- `internal/render.KittyRenderer` can produce fixed-cell Kitty graphics output for prepared cached PNG assets in supported terminals.
- Image loading and rendering must be capability-driven and non-blocking.
- The chat UI must remain usable when image rendering is disabled, unsupported, still loading, or failed.
- Known limitation: default live startup does not yet install the concrete asset resolver/downloader/preparer/renderer stack, and Kitty/Ghostty inline drawing still needs manual terminal validation in a compatible terminal.

## Support Tiers

Tier 1:

- Kitty.
- Ghostty or other Kitty graphics protocol compatible terminals.
- Expected to support inline images through the Kitty-compatible renderer path; manual terminal validation is still required.

Tier 2:

- Modern terminals with true color, mouse support, and Unicode, but no Kitty images.
- Expected to use colored text, initials, native Unicode emoji, and emote labels.

Tier 3:

- Basic terminals with reduced visual capability.
- Expected to use compact text fallbacks and reduced motion where needed.

## Image Feature Status

Overall status: partial. The renderer, cache boundary, public image downloader,
bounded image decode/cell preparation, fixed-width prepared cell substitution,
capability decisions, and visible-row asset event path are implemented. Default
live resolver/downloader/preparer/renderer wiring and manual Kitty/Ghostty
validation are still planned.

Avatars:

- Current: resolve Twitch user `profile_image_url` metadata through Helix Get Users for visible live-chat authors when `avatar_mode = "image"` and Twitch API credentials are configured.
- Current: cache profile metadata by user ID and login and keep initials/user-chip fallbacks stable.
- Current: downloaded PNG/JPEG/GIF avatar assets can be normalized to fixed-width PNG cells through the async image preparation/render boundaries.
- Partial: app asset events can substitute prepared fixed-width cells when an asset resolver, preparer, and renderer are installed.
- Planned: default live startup wiring and manual Kitty/Ghostty validation.

Twitch emotes:

- Current: parse emote positions from Twitch IRC tags and preserve compact fallback tokens.
- Current: resolve Twitch emote metadata and CDN template URLs into cached public image refs.
- Current: downloaded PNG/JPEG/GIF emote assets can be normalized to fixed-width PNG cells through the async image preparation/render boundaries.
- Partial: prepared cells can replace fallback tokens where supported and available.
- Planned: default live startup wiring and manual Kitty/Ghostty validation.

Standard emoji:

- Current: preserve emoji grapheme clusters as native Unicode fallback text.
- Current: map emoji grapheme clusters to provider-neutral, URL-free asset keys.
- Current: resolve standard emoji asset keys to public provider metadata with URL-free cache keys.
- Current: downloaded PNG/JPEG/GIF emoji assets can be normalized to fixed-width PNG cells through the async image preparation/render boundaries.
- Planned: default live startup wiring and manual Kitty/Ghostty validation.

Badges:

- Current: resolve/cache Twitch badge metadata into public image refs and compact labels.
- Current: downloaded PNG/JPEG/GIF badge assets can be normalized to fixed-width PNG cells through the async image preparation/render boundaries.
- Partial: prepared cells can replace compact labels where supported and available.
- Planned: default live startup wiring and manual Kitty/Ghostty validation.

## Capability Decisions

The app now resolves image capability before enabling image-backed app work. The
decision combines:

- Kitty graphics protocol support.
- True color support.
- Cache directory writability.
- Config and environment image mode settings.

Resolved states:

- `enabled`: image mode is active and the terminal/cache hints are suitable.
- `disabled`: image mode is explicitly off or Kitty image support is disabled.
- `unsupported`: `auto` mode found no Kitty/Ghostty signal, so text fallbacks
  remain active.
- `degraded`: explicit image mode or a supported terminal has missing true-color
  or writable-cache signals; fallbacks remain available.

Inline image drawing has a bounded decode/preparation step, renderer core,
row-level substitution point, and Bubble Tea asset-event path for visible rows.
Default live resolver wiring and manual Kitty/Ghostty validation are still
planned.

## Configuration

Implemented controls for fallback rendering and diagnostics:

```sh
TWI_ENABLE_KITTY_IMAGES=true
TWI_IMAGE_MODE=auto
TWI_AVATAR_MODE=initials
TWI_EMOJI_MODE=unicode
TWI_EMOJI_PROVIDER=twemoji
TWI_EMOJI_URL_TEMPLATE=
TWI_EMOTE_MODE=text
```

Recognized mode strings:

- Image: `auto`, `off`, `small`, `normal`, `large`
- Avatar: `off`, `initials`, `image`
- Emoji: `unicode`, `image`
- Emoji provider: `twemoji`, or `custom` with a public URL template containing `{id}`
- Emote: `text`, `image`

The current chat UI uses fallbacks until asset events provide prepared cells.
Mode strings are loaded, reported by diagnostics, and resolved into
deterministic app capability state; image-backed modes reserve stable
placeholders before cells are available.

## Rendering Rules

- Do not block chat rendering while assets load.
- Reserve stable layout width for image placeholders so late image loads do not shift chat rows.
- Keep fallbacks visually intentional, not raw debug labels.
- Preserve Unicode and emoji text when images are unavailable.
- Treat unsupported media, corrupt bytes, unsafe image paths/keys, and oversized images as stable fallback states for the same downloaded record and payload identity; retry transient resolver, downloader, cache, filesystem, context failures, and changed downloaded bytes.
- Keep typed-in reveal animation fragment-aware so it does not split grapheme clusters, ANSI styles, emote tokens, emoji, or image placeholders.
- Degrade to reduced or off animation if rendering falls behind.

## Cache Rules

Cache location:

```text
$XDG_CACHE_HOME/twi
~/.cache/twi
```

Cache behavior should:

- Prune expired entries and fall back to a default 30-day max age when a
  provider-specific expiry is unavailable.
- Prune oldest payloads first when the asset cache exceeds its default
  512 MiB size budget.
- Use ETag or Last-Modified where practical.
- Avoid refetching avatars for every message from the same user.
- Batch Twitch user lookups to avoid API limits.
- Derive on-disk paths from hashed cache keys instead of original IDs, source URLs, or credential-bearing request data.
- Reject local/private image source URLs, credential-bearing URL values, auth headers, cookie headers, and unsafe cache paths before writing downloaded bytes.
- Never store OAuth tokens, client secrets, or credential-bearing URLs.

## Troubleshooting Targets

`twi doctor` currently distinguishes:

- Kitty/Ghostty graphics signals detected from environment hints.
- Kitty graphics unavailable or unknown, with text fallbacks active.
- Image capability enabled, disabled, unsupported, or degraded.
- Image mode disabled by config.
- Cache directory writable or not writable.
- Asset cache pruning complete, timed out, or failed because cleanup needs
  permission/path attention.

Future diagnostics should also distinguish:

- Asset download failed.
- Helix metadata lookup failed.
- Image render failed but fallback is active.

Unsupported image rendering should not prevent chat read, send, scrolling, or composition.

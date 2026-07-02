# Terminal Images

`twi` is planned to support inline images for avatars, Twitch emotes, and standard emoji in capable terminals. The Kitty-compatible renderer core exists behind the internal image renderer boundary, and chat rows can now reserve image placeholders and substitute prepared image cells without changing fallback text. The current MVP implements ready text, Unicode, initials, compact badge, and emote-token fallbacks; live avatar URL metadata can be batched and cached, and visible-row asset events can prepare fixed-width image cells without blocking input.

## Current State

- Text, Unicode, initials, and compact badge fallbacks are implemented for chat rows.
- Renderer asset fallback fragments can reserve stable cell widths before images are available.
- Chat row generation can attach prepared renderer cells by stable URL-free asset key. Unsupported terminals, image-off mode, missing assets, and render failures keep the same fallback rows and reserved widths.
- `internal/storage.AssetCache` provides context-aware cache methods. The in-memory implementation is intended for deterministic tests, and `internal/storage.DiskAssetCache` persists metadata plus cache-owned bytes under the platform cache directory using deterministic hashed paths.
- `twi doctor` reports image-related readiness through terminal color hints, Kitty/Ghostty environment signals, cache writability, selected image/avatar/emoji/emote modes, and the resolved image capability state.
- `internal/render.KittyRenderer` can produce fixed-cell Kitty graphics output for prepared cached PNG assets in supported terminals.
- Image loading and rendering must be capability-driven and non-blocking.
- The chat UI must remain usable when image rendering is disabled, unsupported, still loading, or failed.
- Known limitation: default live startup does not yet install a concrete asset resolver/downloader/renderer stack, and Kitty/Ghostty inline drawing still needs manual terminal validation.

## Support Tiers

Tier 1:

- Kitty.
- Ghostty or other Kitty graphics protocol compatible terminals.
- Expected to support inline images once the renderer is integrated into chat rows.

Tier 2:

- Modern terminals with true color, mouse support, and Unicode, but no Kitty images.
- Expected to use colored text, initials, native Unicode emoji, and emote labels.

Tier 3:

- Basic terminals with reduced visual capability.
- Expected to use compact text fallbacks and reduced motion where needed.

## Planned Image Features

Avatars:

- Resolve Twitch user `profile_image_url` values through Helix Get Users. This metadata step is implemented for visible live-chat authors when `avatar_mode = "image"` and Twitch API credentials are configured.
- Cache profile metadata by user ID and login.
- Download and cache image assets.
- Render fixed-size inline images where supported.
- Fall back to initials or user chips without changing message layout.

Twitch emotes:

- Parse emote positions from Twitch IRC tags.
- Resolve emote metadata and CDN template URLs.
- Cache metadata and image assets.
- Render image emotes where supported.
- Fall back to compact text tokens.

Standard emoji:

- Detect emoji grapheme clusters.
- Resolve local or provider-backed emoji image assets.
- Render emoji images where supported.
- Fall back to native Unicode emoji.

Badges:

- Cache global and channel badge metadata.
- Render as compact labels or icons.
- Keep text fallbacks available.

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

Inline image drawing has a renderer core, row-level substitution point, and
Bubble Tea asset-event path for visible rows. Default live resolver wiring and
manual terminal validation are still planned.

## Configuration

Implemented controls for fallback rendering and diagnostics:

```sh
TWI_ENABLE_KITTY_IMAGES=true
TWI_IMAGE_MODE=auto
TWI_AVATAR_MODE=initials
TWI_EMOJI_MODE=unicode
TWI_EMOTE_MODE=text
```

Recognized mode strings:

- Image: `auto`, `off`, `small`, `normal`, `large`
- Avatar: `off`, `initials`, `image`
- Emoji: `unicode`, `image`
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

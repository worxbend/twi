# Terminal Images

`twi` is planned to support inline images for avatars, Twitch emotes, and standard emoji in capable terminals. The current MVP implements stable text, Unicode, initials, and compact badge fallbacks before inline image loading/rendering is added.

## Current State

- Text, Unicode, initials, and compact badge fallbacks are implemented for chat rows.
- Renderer asset fallback fragments can reserve stable cell widths before images are available.
- `internal/storage.AssetCache` provides context-aware cache methods; the in-memory implementation is intended for deterministic tests and performs no network access.
- Kitty-compatible image rendering is the first planned image protocol target.
- Image loading and rendering must be capability-driven and non-blocking.
- The chat UI must remain usable when image rendering is disabled, unsupported, still loading, or failed.

## Support Tiers

Tier 1:

- Kitty.
- Ghostty or other Kitty graphics protocol compatible terminals.
- Expected to support inline images once the renderer is implemented.

Tier 2:

- Modern terminals with true color, mouse support, and Unicode, but no Kitty images.
- Expected to use colored text, initials, native Unicode emoji, and emote labels.

Tier 3:

- Basic terminals with reduced visual capability.
- Expected to use compact text fallbacks and reduced motion where needed.

## Planned Image Features

Avatars:

- Resolve Twitch user `profile_image_url` values through Helix Get Users.
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

## Capability Detection

The app should detect terminal image capability before enabling image rendering. Planned detection includes:

- Kitty graphics protocol support.
- True color support.
- Cache directory writability.
- Config and environment image mode settings.

`twi doctor` now reports terminal color hints, Kitty/Ghostty environment
signals, cache directory writability, and the selected image/avatar/emoji/emote
modes. These are diagnostic hints only; inline image rendering is still behind
future asset and terminal-renderer work.

## Configuration

Planned controls:

```sh
TWI_ENABLE_KITTY_IMAGES=true
TWI_IMAGE_MODE=normal
TWI_AVATAR_MODE=initials
TWI_EMOJI_MODE=unicode
TWI_EMOTE_MODE=text
```

Planned modes:

- Image: `off`, `small`, `normal`, `large`
- Avatar: `off`, `initials`, `image`
- Emoji: `unicode`, `image`
- Emote: `text`, `image`

The MVP currently uses fallbacks only. Image modes should become effective when the asset pipeline and terminal renderer are implemented.

## Rendering Rules

- Do not block chat rendering while assets load.
- Reserve stable layout width for image placeholders so late image loads do not shift chat rows.
- Keep fallbacks visually intentional, not raw debug labels.
- Preserve Unicode and emoji text when images are unavailable.
- Keep typed-in reveal animation fragment-aware so it does not split grapheme clusters, ANSI styles, emote tokens, emoji, or image placeholders.
- Degrade to reduced or off animation if rendering falls behind.

## Cache Rules

Planned cache location:

```text
$XDG_CACHE_HOME/twi
~/.cache/twi
```

Cache behavior should:

- Bound size and age.
- Use TTLs.
- Use ETag or Last-Modified where practical.
- Avoid refetching avatars for every message from the same user.
- Batch Twitch user lookups to avoid API limits.
- Never store OAuth tokens or client secrets.

## Troubleshooting Targets

`twi doctor` currently distinguishes:

- Kitty/Ghostty graphics signals detected from environment hints.
- Kitty graphics unavailable or unknown, with text fallbacks active.
- Image mode disabled by config.
- Cache directory writable or not writable.

Future diagnostics should also distinguish:

- Asset download failed.
- Helix metadata lookup failed.
- Image render failed but fallback is active.

Unsupported image rendering should not prevent chat read, send, scrolling, or composition.

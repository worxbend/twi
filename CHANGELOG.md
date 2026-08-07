# Changelog

All notable changes to `twi` are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the major version is `0`, minor releases may change behavior.

Versions are derived from git tags (`git describe`); there is no version
constant in the source tree.

## [Unreleased]

### Added

- **`scrollback_limit` config key** (`TWI_SCROLLBACK_LIMIT`, default `2000`).
  Caps retained messages per channel. Set `0` to keep everything, at the cost
  of a repaint that slows down as the buffer grows.

- **First-time chatters are marked.** A `✦` before the name on a chatter's
  very first message in the channel, from Twitch's `first-msg` tag — the only
  reliable source, since a local roster cannot know whether someone has
  visited before. Hidden in `compact` layout and below 24 columns, where the
  space belongs to the message.

### Changed

- **`twi chat` and `twi doctor` now warn when OAuth refresh cannot run.**
  `twi login` saves a refresh token and client ID but no client secret, and
  the refresh flow needs all three — so the documented setup held a refresh
  token it could never redeem, and live chat simply stopped when the access
  token expired (about 4 hours) with nothing having warned about it. Doctor's
  **client secret** check now names the consequence instead of calling the
  flow "optional", and reports which credential is missing rather than a bare
  "unavailable".

### Fixed

- **Chat no longer slows down over a long stream.** Every repaint re-rendered
  the entire retained backlog, and the backlog was never trimmed, so frame
  time grew with session length: `View()` measured 65ms at 1,000 retained
  messages and 325ms at 5,000, against a 100ms animation tick. Rendered rows
  are now memoized per message, only the visible window is styled, and the
  backlog is capped. The same measurements are now 4.1ms and 11.5ms.
- **A busy chat can no longer stall the connection.** `emitMessage` was the
  one emitter that blocked when its buffer filled, so a UI falling behind
  during a raid back-pressured into the goroutine that answers Twitch's
  keepalives — and Twitch dropped the connection. It now discards the oldest
  queued message instead, and the status bar reports `dropped=N` so the loss
  is visible rather than silent.
- **Messages that open with a long mention or emote keep their author.** A
  wrapping branch discarded the row it was abandoning. On the content pass
  that row is the one holding the timestamp, badges and name, so a message
  starting with a mention too wide to sit beside the prefix rendered with no
  author at all — at ordinary terminal widths, not just extreme ones.
- **Chat wraps between words instead of through them.** A long message used
  to break mid-word (`one two th` / `ree four f` / `ive six`). Words wider
  than the line still wrap by character, so nothing becomes unrenderable.
- **Chat reconnects on its own.** When Twitch closed the connection — a
  server restart, a momentary blip — chat stayed dead until someone noticed
  the silence and pressed `ctrl+r`, which mid-stream can be a long time. It
  now retries automatically with exponential backoff (2s doubling to 60s,
  giving up after roughly ten minutes) and says so if it gives up.
- **Sending too fast is refused locally instead of costing the connection.**
  Twitch allows 20 chat messages per 30 seconds and closes the connection —
  not just the message — when you exceed it. `SendResult.RateLimited` and the
  composer's rate-limited state both already existed with nothing setting
  them, so hitting the ceiling looked like a successful send. Twitch's
  duplicate-message rule is enforced the same way.
- **Sending on a dead connection no longer reports success.** The IRC library
  queues a message and returns nothing, so `Send` always reported the message
  as accepted even with the socket down; it was written into a buffer and
  never reached Twitch. Sends are now refused while disconnected.
- **Chat recovers from token expiry instead of dying at it.** A refresh
  rotates both tokens, but the reconnect path had captured the old ones, so
  `ctrl+r` — the recovery key the UI names — retried with credentials Twitch
  had already invalidated. Only one refresh was possible per process, so a
  session outliving two expiries ended with no way back. All ten Helix-backed
  features (LIVE indicator, follower/subscriber counts, `/clip`, markers,
  Stream Info, emote index) also froze the startup token and began returning
  401 after a refresh while chat kept working. One shared credential holder
  now serves all of them, and the refresh POST has a 15-second timeout where
  it previously had none.
- **Ordinary Twitch notices no longer look like auth failures.** Errors were
  classified by searching for `auth`, `invalid`, `permission` or `scope` in
  the message text, so `x509: certificate signed by unknown authority` was
  reported as a bad OAuth token, and a `no_permission` notice turned the
  status bar red on a perfectly healthy connection.
- **Deleting a message no longer reprints it.** Twitch's `CLEARMSG` carries
  the deleted message's text, and twi rendered that into a new visible notice
  while leaving the original in place — so moderating a message put its text
  on screen twice. Deletions, bans, timeouts and chat clears now redact the
  messages already on screen instead.
- **Outbound chat text is sanitized.** Carriage returns and newlines in a sent
  message could end the IRC command early, letting the remainder be parsed as
  a new command. Line breaks are now collapsed, other control characters
  dropped, and messages capped at Twitch's 500-character limit.
- **`message_layout`, `badge_mode`, `highlight_emotes` and `full_username` now
  apply to live chat.** The live and mock models were constructed separately
  and the live one never read these four settings, so they worked in
  `--mock` and did nothing against real Twitch. If you set any of them, live
  chat will now look the way it was already configured to.

## [0.14.0] — 2026-08-07

### Added

- **Animated text effects for chrome labels.** `internal/animation` gained a
  text-effect engine with four treatments — `typewriter` (caret reveal),
  `gradient-wave`, `shimmer`, and `bounce` (a marker with a fading trail) —
  rendered as a pure function of elapsed time on the existing shared ~10fps
  clock. Effects return styled cells rather than escape sequences, so the
  package keeps its terminal-free boundary.
- The **splash tagline now types in behind a blinking caret** and the
  strapline below it shimmers, both centered at their final width so the
  words no longer slide sideways as they appear.
- The **no-channels-open empty state** drifts an accent gradient through its
  headline and bounces an idle marker below the hints, so a waiting pane
  still reads as a live app.
- **20 more themes**, taking the built-in set from 13 to 33. Dark:
  `catppuccin-macchiato`, `catppuccin-frappe`, `rose-pine-moon`, `everforest`,
  `kanagawa`, `ayu-dark`, `ayu-mirage`, `night-owl`, `palenight`,
  `synthwave-84`, `oceanic-next`, `nightfox`, `zenburn`, `cobalt2`,
  `horizon`. Light — twi's first: `catppuccin-latte`, `rose-pine-dawn`,
  `gruvbox-light`, `solarized-light`, `github-light`. All are selectable from
  `ctrl+t`, `theme_name`, `TWI_THEME_NAME`, and `twi profile set`.
- Preset palettes are now covered by contrast tests: every role must be valid
  hex, body text must clear 4.5:1 on both the background and the pane
  surface, and the accent must clear 3:1 on the background.

### Changed

- Every text effect preserves its label's display width on every frame, and
  `animation_mode = "off"` renders the same wording and layout statically;
  `"reduced"` roughly halves the motion rather than disabling it.

## [0.13.0] — 2026-08-02

### Added

- **Channels can be opened and closed while twi is running.** `/channels` (or
  `space` `c`) opens a searchable picker that autocompletes from the channels
  you follow, lists what is already open, and accepts any typed name;
  `/channels somechannel` skips the picker. Opening joins on the existing IRC
  connection and closing parts it, so neither needs a reconnect.
- **`--channels a,b`**, a comma-separated companion to the repeatable
  `--channel`, on both `twi chat` and `twi setup`. The two accumulate.
- **Starting with no channel.** `twi chat` without a configured channel no
  longer exits; it opens on an empty state that names the ways to open one.
  Closing the last channel returns there rather than quitting.
- **`user:read:follows`** is now requested at login, backing the picker's
  autocomplete through Twitch Helix "Get Followed Channels". Tokens issued
  before this change keep working — the picker says the follow list is
  unavailable and falls back to open and configured channels until you run
  `twi login` again.
- **Mouse navigation across the shell**: clicking a tab switches screens,
  clicking a sidebar channel switches to it, clicking its `✕` closes it, and
  clicking a row in the command palette or a picker runs that entry.

### Changed

- **Vim/AstroNvim-style keys.** `i`, `o`, and `a` focus the composer; `esc`
  leaves it for the chat view with the draft intact; `j`/`k` select messages
  and move the sidebar highlight. The inspect panel moved from `i` to `K`
  (also `space` `i`), since `i` now means insert. `space` is a leader chord
  outside the composer: `e` toggles the channel sidebar, `c` opens the channel
  picker, `x` closes the active channel.
- The channel sidebar is now focusable (`tab` when visible) with `j`/`k` to
  move, `enter`/`l` to switch, `x` to close, and `h`/`esc` to leave.

### Removed

- **The permanent emotes quick-select strip on the main screen.** The dashboard
  no longer reserves a row below the composer for browsable emotes; that space
  now goes to chat. `ctrl+e` still opens the searchable emote/emoji picker,
  which remains the way to insert an emote. `tab` now cycles between chat and
  the composer only, and `left`/`right` no longer move an emote selection.

## [0.12.1] — 2026-08-01

Documentation and website only. No behavior changes; the `v0.12.0` binaries
remain current.

### Added

- This changelog.
- A "What's new" section on the [project website](https://worxbend.github.io/twi/).

### Changed

- Documented that `twitch_username` is optional and derived from the OAuth
  token, across the README, quickstart, auth, config, and troubleshooting docs.

## [0.12.0] — 2026-08-01

### Changed

- **The IRC login is derived from the OAuth token instead of config.** Twitch
  requires the IRC login to be the account a token was issued to, and the
  validation response already reports that account, so honoring a configured
  `twitch_username` that disagreed could only ever produce a login rejection.
  `twitch_username` is now optional and acts as a fallback for when token
  validation cannot reach Twitch.
- A `twitch_username` naming a different account is now a warning that names
  both accounts, not a startup failure. `twi doctor` reports it as stale config
  rather than as a token problem.

  This removes a confusing failure mode: signing in as one account and chatting
  in another channel is normal and always was allowed — the channels you join
  have never been tied to the account you authenticate as.

### Added

- A [project website](https://worxbend.github.io/twi/) with screenshots, the
  full theme set, an install path, and a keyboard reference, deployed to GitHub
  Pages from `site/`.
- Terminal screenshots generated from twi itself: `TestWriteDocsScreenshots`
  renders the real view and converts the ANSI output to SVG, so the images
  cannot drift from what the app prints. Regenerate with
  `TWI_WRITE_SCREENSHOTS=1 go test ./internal/app -run TestWriteDocsScreenshots`.

### Fixed

- Six pre-existing static-analysis findings that had been failing the CI quality
  gate on every push since mid-July.

### Unchanged

- Missing `chat:read`/`chat:edit` scopes still stop startup. Scopes are a real
  blocker; the username was not.

## [0.11.0] — 2026-08-01

### Added

- **Chatter roster.** Twitch membership (`JOIN`/`PART`) is now normalized into a
  per-channel roster that backs mention autocomplete, author metadata, the
  active-chatter count, and join/leave activity rows. It is best-effort by
  construction: Twitch batches membership, delays it, and stops sending it
  entirely for busy channels, so presence falls back to a message-recency
  window when membership is silent.
- **`@mention` autocomplete.** Type `@` and a prefix to complete from people
  actually in chat, ranked by who spoke most recently. <kbd>tab</kbd> accepts,
  arrows move, <kbd>esc</kbd> dismisses for that word only. The strip claims
  those keys only while it is open, so <kbd>enter</kbd> still sends.
- **Three message layouts** — `grouped` (one author header per run of
  messages), `inline` (the dense classic), and `compact` (text only) — cycled
  with <kbd>ctrl+g</kbd>.
- **Glyph badges** for broadcaster, moderator, VIP, subscriber and more,
  cycled with <kbd>ctrl+b</kbd> between `glyph`, `text`, and `off`.
- **Emote and emoji highlight chips**, toggled with <kbd>ctrl+y</kbd>.
- **Full usernames** (`DisplayName (login)`), toggled with <kbd>ctrl+n</kbd>.
- **Author context** beside each message: role, subscriber tenure, follow age,
  and how long twi has seen that person. Anything twi does not actually know is
  omitted rather than guessed — an absent "follows" note means "no follower
  data", never "does not follow".
- **A live active-chatter count** in the chat pane title, marked `~` when it is
  inferred from recency rather than membership.
- **Join/leave rows in the activity column**, with per-kind glyphs. Reconnect
  bursts collapse into a single rolling summary row rather than hundreds of
  lines.
- **Mouse focus for the emotes strip**, including selecting the emote under the
  cursor.
- New settings: `message_layout`, `badge_mode`, `highlight_emotes`, and
  `full_username`, each with a `TWI_`-prefixed environment variable. Runtime
  toggles persist to the config file; a failed write keeps the change and says
  so.

### Changed

- **Per-user color now carries into the message surface and gutter rail**, not
  just the nickname, so a person's block is recognizable at a glance. Notices
  and system rows keep neutral treatment rather than being tinted by a
  fabricated identity.
- **The theme picker is a full-screen page** rather than a strip docked under
  the chat, listing every preset with a swatch strip of its own palette.
  <kbd>home</kbd>/<kbd>end</kbd> jump to the ends of the list.
- **Badges default to glyphs** instead of bracketed `[moderator]` labels. Set
  `badge_mode = "text"` or press <kbd>ctrl+b</kbd> to restore the old look.
- **`?` no longer toggles help while the composer has focus**, where it is an
  ordinary character in the message being typed.

### Fixed

- **Request the `twitch.tv/membership` IRC capability.** twi overrode the
  library default with tags and commands only, so `JOIN`/`PART` never arrived.
- **Attach the user's own badges, display name, and color to local echoes**,
  sourced from `USERSTATE`. Twitch never echoes a user's own message back, so
  the broadcaster was the one person in chat whose own badge never rendered.

## [0.10.0] — 2026-07-17

### Added

- Grouped chat messages and refined UI motion.

## [0.9.0] — 2026-07-17

### Changed

- Refreshed TUI surfaces and nickname colors.

## [0.8.0] — 2026-07-17

### Changed

- Polished animated chat visuals.

---

Releases before `0.8.0` predate this changelog; see the
[commit history](https://github.com/worxbend/twi/commits/main) and the
[releases page](https://github.com/worxbend/twi/releases).

[Unreleased]: https://github.com/worxbend/twi/compare/v0.14.0...HEAD
[0.14.0]: https://github.com/worxbend/twi/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/worxbend/twi/compare/v0.12.1...v0.13.0
[0.12.1]: https://github.com/worxbend/twi/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/worxbend/twi/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/worxbend/twi/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/worxbend/twi/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/worxbend/twi/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/worxbend/twi/releases/tag/v0.8.0

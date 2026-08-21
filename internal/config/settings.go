package config

import (
	"strconv"
	"strings"
)

// setting describes one configuration setting: what a config file calls it,
// which environment variable sets it, how a text value is applied to a
// Config, and how its current value is written back out.
//
// Every setting is described exactly once, here. This list used to be spelled
// out five separate times -- in applyKey for config files, in applyEnv for
// environment variables, and twice over in WriteNonSecretFile (once as a map
// of values and again as a slice fixing their order) -- so adding a setting
// meant finding all of them and keeping them consistent. They had already
// drifted apart.
type setting struct {
	// key names the setting in a config file, e.g. "theme_accent".
	key string
	// env names the environment variable that sets it.
	env string
	// apply writes a text value, read from a file or the environment, into
	// cfg. Each setting parses its own value, so the table also records
	// which settings are booleans, integers or lists.
	apply func(cfg *Config, value string)
	// format renders the setting's current value the way a config file
	// spells it. It is nil for secrets, which are never written to a file.
	format func(cfg Config) string
	// persisted marks the settings the config writer keeps in config.toml.
	// Credentials are excluded (they live in the credential store), as are
	// the debug and pane-size settings, which are deliberately not written
	// back by the starter config.
	persisted bool
}

// settings is every configuration setting, in the order a written config file
// lists them.
//
// The environment variable is the key uppercased behind a TWI_ prefix for all
// but debug_logging, which predates that convention and stayed TWI_DEBUG_LOG
// so existing setups keep working.
var settings = []setting{
	{
		key: "twitch_username", env: "TWI_TWITCH_USERNAME", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Twitch.Username = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Twitch.Username) },
	},
	{
		key: "twitch_oauth_token", env: "TWI_TWITCH_OAUTH_TOKEN",
		apply: func(cfg *Config, v string) { cfg.Twitch.OAuthToken = v },
	},
	{
		key: "twitch_refresh_token", env: "TWI_TWITCH_REFRESH_TOKEN",
		apply: func(cfg *Config, v string) { cfg.Twitch.RefreshToken = v },
	},
	{
		key: "twitch_client_id", env: "TWI_TWITCH_CLIENT_ID", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Twitch.ClientID = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Twitch.ClientID) },
	},
	{
		key: "twitch_client_secret", env: "TWI_TWITCH_CLIENT_SECRET",
		apply: func(cfg *Config, v string) { cfg.Twitch.ClientSecret = v },
	},
	{
		key: "twitch_redirect_url", env: "TWI_TWITCH_REDIRECT_URL", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Twitch.RedirectURL = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Twitch.RedirectURL) },
	},
	{
		key: "default_channels", env: "TWI_DEFAULT_CHANNELS", persisted: true,
		apply: func(cfg *Config, v string) { cfg.DefaultChannels = splitList(v) },
		format: func(cfg Config) string {
			return quote(strings.Join(normalizeChannels(cfg.DefaultChannels), ","))
		},
	},
	{
		key: "enable_mouse", env: "TWI_ENABLE_MOUSE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.EnableMouse = parseBool(v, cfg.Features.EnableMouse) },
		format: func(cfg Config) string { return strconv.FormatBool(cfg.Features.EnableMouse) },
	},
	{
		key: "avatar_mode", env: "TWI_AVATAR_MODE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.AvatarMode = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.AvatarMode) },
	},
	{
		key: "animation_mode", env: "TWI_ANIMATION_MODE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.AnimationMode = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.AnimationMode) },
	},
	{
		key: "theme_name", env: "TWI_THEME_NAME", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeName = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeName) },
	},
	{
		key: "theme_background", env: "TWI_THEME_BACKGROUND", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Background = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Background) },
	},
	{
		key: "theme_foreground", env: "TWI_THEME_FOREGROUND", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Foreground = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Foreground) },
	},
	{
		key: "theme_accent", env: "TWI_THEME_ACCENT", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Accent = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Accent) },
	},
	{
		key: "theme_muted", env: "TWI_THEME_MUTED", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Muted = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Muted) },
	},
	{
		key: "theme_border", env: "TWI_THEME_BORDER", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Border = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Border) },
	},
	{
		key: "theme_surface", env: "TWI_THEME_SURFACE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Surface = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Surface) },
	},
	{
		key: "theme_warning", env: "TWI_THEME_WARNING", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Warning = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Warning) },
	},
	{
		key: "theme_error", env: "TWI_THEME_ERROR", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Error = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Error) },
	},
	{
		key: "theme_success", env: "TWI_THEME_SUCCESS", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.ThemeCustom.Success = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.ThemeCustom.Success) },
	},
	{
		key: "stream_status_mode", env: "TWI_STREAM_STATUS_MODE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.StreamStatusMode = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.StreamStatusMode) },
	},
	{
		key: "emote_autocomplete_mode", env: "TWI_EMOTE_AUTOCOMPLETE_MODE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.EmoteAutocompleteMode = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.EmoteAutocompleteMode) },
	},
	{
		key: "message_layout", env: "TWI_MESSAGE_LAYOUT", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.MessageLayout = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.MessageLayout) },
	},
	{
		key: "badge_mode", env: "TWI_BADGE_MODE", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.BadgeMode = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Features.BadgeMode) },
	},
	{
		key: "highlight_emotes", env: "TWI_HIGHLIGHT_EMOTES", persisted: true,
		apply: func(cfg *Config, v string) {
			cfg.Features.HighlightEmotes = parseBool(v, cfg.Features.HighlightEmotes)
		},
		format: func(cfg Config) string { return strconv.FormatBool(cfg.Features.HighlightEmotes) },
	},
	{
		key: "full_username", env: "TWI_FULL_USERNAME", persisted: true,
		apply:  func(cfg *Config, v string) { cfg.Features.FullUsername = parseBool(v, cfg.Features.FullUsername) },
		format: func(cfg Config) string { return strconv.FormatBool(cfg.Features.FullUsername) },
	},
	{
		key: "sidebar_width", env: "TWI_SIDEBAR_WIDTH",
		apply:  func(cfg *Config, v string) { cfg.Features.SidebarWidth = parseInt(v, cfg.Features.SidebarWidth) },
		format: func(cfg Config) string { return strconv.Itoa(cfg.Features.SidebarWidth) },
	},
	{
		key: "activity_width", env: "TWI_ACTIVITY_WIDTH",
		apply:  func(cfg *Config, v string) { cfg.Features.ActivityWidth = parseInt(v, cfg.Features.ActivityWidth) },
		format: func(cfg Config) string { return strconv.Itoa(cfg.Features.ActivityWidth) },
	},
	{
		key: "scrollback_limit", env: "TWI_SCROLLBACK_LIMIT",
		apply: func(cfg *Config, v string) {
			cfg.Features.ScrollbackLimit = parseInt(v, cfg.Features.ScrollbackLimit)
		},
		format: func(cfg Config) string { return strconv.Itoa(cfg.Features.ScrollbackLimit) },
	},
	{
		key: "debug_logging", env: "TWI_DEBUG_LOG",
		apply:  func(cfg *Config, v string) { cfg.Debug.Enabled = parseBool(v, cfg.Debug.Enabled) },
		format: func(cfg Config) string { return strconv.FormatBool(cfg.Debug.Enabled) },
	},
	{
		key: "debug_log_path", env: "TWI_DEBUG_LOG_PATH",
		apply:  func(cfg *Config, v string) { cfg.Debug.LogPath = v },
		format: func(cfg Config) string { return quoteTrimmed(cfg.Debug.LogPath) },
	},
}

// settingsByKey and settingsByEnv index settings for lookup while reading a
// config file and the environment.
var settingsByKey, settingsByEnv = func() (map[string]setting, map[string]setting) {
	byKey := make(map[string]setting, len(settings))
	byEnv := make(map[string]setting, len(settings))
	for _, s := range settings {
		byKey[s.key] = s
		byEnv[s.env] = s
	}
	return byKey, byEnv
}()

// quoteTrimmed renders a string setting for a config file.
func quoteTrimmed(value string) string {
	return quote(strings.TrimSpace(value))
}

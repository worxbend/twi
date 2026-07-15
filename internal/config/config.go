package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/worxbend/twi/internal/theme"
)

const redacted = "[redacted]"

// Config is the effective application configuration after merging defaults,
// config file values, environment variables, and CLI overrides.
type Config struct {
	Path            string
	Twitch          TwitchConfig
	DefaultChannels []string
	Features        FeatureConfig
	Debug           DebugConfig
}

type TwitchConfig struct {
	Username     string
	OAuthToken   string
	RefreshToken string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type FeatureConfig struct {
	EnableKittyImages     bool
	EnableMouse           bool
	ImageMode             string
	AvatarMode            string
	EmojiMode             string
	EmojiProvider         string
	EmojiURLTemplate      string
	EmoteMode             string
	AnimationMode         string
	ThemeName             string
	ThemeCustom           theme.Palette
	StreamStatusMode      string
	EmoteAutocompleteMode string
}

// ResolveTheme returns the effective palette for cfg: the named preset, or
// the custom palette when theme_name is "custom".
func (c Config) ResolveTheme() theme.Palette {
	palette, _ := theme.ResolvePalette(c.Features.ThemeName, c.Features.ThemeCustom)
	return palette
}

type DebugConfig struct {
	Enabled bool
	LogPath string
}

type Overrides struct {
	ConfigPath      string
	Channels        []string
	DebugLogSet     bool
	DebugLogEnabled bool
	DebugLogPath    string
}

// Load returns the effective config. Precedence is flags/overrides > env >
// config file > defaults.
func Load(environ []string, overrides Overrides) (Config, error) {
	cfg := Default()

	path := overrides.ConfigPath
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return Config{}, err
		}
		path = defaultPath
	}
	cfg.Path = path

	if err := applyFile(&cfg, path); err != nil {
		return Config{}, err
	}
	applyEnv(&cfg, environ)
	if len(overrides.Channels) > 0 {
		cfg.DefaultChannels = normalizeChannels(overrides.Channels)
	}
	applyOverrides(&cfg, overrides)

	return cfg, nil
}

func LoadEnvOnly(environ []string, overrides Overrides) (Config, error) {
	cfg := Default()

	path := overrides.ConfigPath
	if path == "" {
		defaultPath, err := DefaultPath()
		if err != nil {
			return Config{}, err
		}
		path = defaultPath
	}
	cfg.Path = path

	applyEnv(&cfg, environ)
	if len(overrides.Channels) > 0 {
		cfg.DefaultChannels = normalizeChannels(overrides.Channels)
	}
	applyOverrides(&cfg, overrides)

	return cfg, nil
}

func Default() Config {
	return Config{
		Features: FeatureConfig{
			EnableKittyImages:     true,
			EnableMouse:           true,
			ImageMode:             "auto",
			AvatarMode:            "initials",
			EmojiMode:             "image",
			EmojiProvider:         "twemoji",
			EmoteMode:             "image",
			AnimationMode:         "fast",
			ThemeName:             "claude",
			StreamStatusMode:      "auto",
			EmoteAutocompleteMode: "auto",
		},
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "twi", "config.toml"), nil
}

func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "twi"), nil
}

// WriteNonSecretFile creates or updates the flat config file with non-secret
// values from cfg. Existing secret keys and unknown lines are preserved
// unchanged, but this function never creates or updates token, refresh-token,
// or client-secret keys.
func WriteNonSecretFile(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config path is required")
	}
	updates := map[string]string{
		"twitch_username":         quote(strings.TrimSpace(cfg.Twitch.Username)),
		"twitch_client_id":        quote(strings.TrimSpace(cfg.Twitch.ClientID)),
		"twitch_redirect_url":     quote(strings.TrimSpace(cfg.Twitch.RedirectURL)),
		"default_channels":        quote(strings.Join(normalizeChannels(cfg.DefaultChannels), ",")),
		"enable_kitty_images":     strconv.FormatBool(cfg.Features.EnableKittyImages),
		"enable_mouse":            strconv.FormatBool(cfg.Features.EnableMouse),
		"image_mode":              quote(strings.TrimSpace(cfg.Features.ImageMode)),
		"avatar_mode":             quote(strings.TrimSpace(cfg.Features.AvatarMode)),
		"emoji_mode":              quote(strings.TrimSpace(cfg.Features.EmojiMode)),
		"emoji_provider":          quote(strings.TrimSpace(cfg.Features.EmojiProvider)),
		"emote_mode":              quote(strings.TrimSpace(cfg.Features.EmoteMode)),
		"animation_mode":          quote(strings.TrimSpace(cfg.Features.AnimationMode)),
		"theme_name":              quote(strings.TrimSpace(cfg.Features.ThemeName)),
		"theme_background":        quote(strings.TrimSpace(cfg.Features.ThemeCustom.Background)),
		"theme_foreground":        quote(strings.TrimSpace(cfg.Features.ThemeCustom.Foreground)),
		"theme_accent":            quote(strings.TrimSpace(cfg.Features.ThemeCustom.Accent)),
		"theme_muted":             quote(strings.TrimSpace(cfg.Features.ThemeCustom.Muted)),
		"theme_border":            quote(strings.TrimSpace(cfg.Features.ThemeCustom.Border)),
		"theme_surface":           quote(strings.TrimSpace(cfg.Features.ThemeCustom.Surface)),
		"theme_warning":           quote(strings.TrimSpace(cfg.Features.ThemeCustom.Warning)),
		"theme_error":             quote(strings.TrimSpace(cfg.Features.ThemeCustom.Error)),
		"theme_success":           quote(strings.TrimSpace(cfg.Features.ThemeCustom.Success)),
		"stream_status_mode":      quote(strings.TrimSpace(cfg.Features.StreamStatusMode)),
		"emote_autocomplete_mode": quote(strings.TrimSpace(cfg.Features.EmoteAutocompleteMode)),
	}
	order := []string{
		"twitch_username",
		"twitch_client_id",
		"twitch_redirect_url",
		"default_channels",
		"enable_kitty_images",
		"enable_mouse",
		"image_mode",
		"avatar_mode",
		"emoji_mode",
		"emoji_provider",
		"emote_mode",
		"animation_mode",
		"theme_name",
		"theme_background",
		"theme_foreground",
		"theme_accent",
		"theme_muted",
		"theme_border",
		"theme_surface",
		"theme_warning",
		"theme_error",
		"theme_success",
		"stream_status_mode",
		"emote_autocomplete_mode",
	}
	return writeFlatConfigUpdates(path, order, updates)
}

func writeFlatConfigUpdates(path string, order []string, updates map[string]string) error {
	var existing string
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		existing = string(data)
	case errors.Is(err, os.ErrNotExist):
	default:
		return err
	}

	seen := map[string]bool{}
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(existing))
	for scanner.Scan() {
		line := scanner.Text()
		if key, ok := configLineKey(line); ok {
			if value, exists := updates[key]; exists {
				lines = append(lines, key+" = "+value)
				seen[key] = true
				continue
			}
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if len(lines) > 0 {
		lines = append(lines, "")
	}
	for _, key := range order {
		if !seen[key] {
			lines = append(lines, key+" = "+updates[key])
		}
	}

	output := strings.Join(lines, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	return writeFilePrivate(path, []byte(output))
}

func configLineKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	key, _, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	return key, true
}

func writeFilePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (c Config) RedactedString() string {
	lines := []string{
		"path = " + quote(RedactDisplayValue(c.Path)),
		"twitch_username = " + quote(c.Twitch.Username),
		"twitch_oauth_token = " + quote(redact(c.Twitch.OAuthToken)),
		"twitch_refresh_token = " + quote(redact(c.Twitch.RefreshToken)),
		"twitch_client_id = " + quote(c.Twitch.ClientID),
		"twitch_client_secret = " + quote(redact(c.Twitch.ClientSecret)),
		"twitch_redirect_url = " + quote(redactUnsafe(c.Twitch.RedirectURL)),
		"default_channels = " + quote(strings.Join(c.DefaultChannels, ",")),
		"enable_kitty_images = " + strconv.FormatBool(c.Features.EnableKittyImages),
		"enable_mouse = " + strconv.FormatBool(c.Features.EnableMouse),
		"image_mode = " + quote(c.Features.ImageMode),
		"avatar_mode = " + quote(c.Features.AvatarMode),
		"emoji_mode = " + quote(c.Features.EmojiMode),
		"emoji_provider = " + quote(c.Features.EmojiProvider),
		"emoji_url_template = " + quote(redactUnsafe(c.Features.EmojiURLTemplate)),
		"emote_mode = " + quote(c.Features.EmoteMode),
		"animation_mode = " + quote(c.Features.AnimationMode),
		"theme_name = " + quote(c.Features.ThemeName),
		"theme_background = " + quote(c.Features.ThemeCustom.Background),
		"theme_foreground = " + quote(c.Features.ThemeCustom.Foreground),
		"theme_accent = " + quote(c.Features.ThemeCustom.Accent),
		"theme_muted = " + quote(c.Features.ThemeCustom.Muted),
		"theme_border = " + quote(c.Features.ThemeCustom.Border),
		"theme_surface = " + quote(c.Features.ThemeCustom.Surface),
		"theme_warning = " + quote(c.Features.ThemeCustom.Warning),
		"theme_error = " + quote(c.Features.ThemeCustom.Error),
		"theme_success = " + quote(c.Features.ThemeCustom.Success),
		"stream_status_mode = " + quote(c.Features.StreamStatusMode),
		"emote_autocomplete_mode = " + quote(c.Features.EmoteAutocompleteMode),
		"debug_logging = " + strconv.FormatBool(c.Debug.Enabled),
		"debug_log_path = " + quote(RedactDisplayValue(c.Debug.LogPath)),
	}
	return strings.Join(lines, "\n") + "\n"
}

func redact(value string) string {
	if value == "" {
		return ""
	}
	return redacted
}

// RedactDisplayValue returns a safe display representation for user-controlled
// non-secret fields that may still contain credential-shaped query values.
func RedactDisplayValue(value string) string {
	return redactUnsafe(value)
}

func redactUnsafe(value string) string {
	if value == "" {
		return ""
	}
	if containsSecretMarker(value) || containsURLUserInfo(value) {
		return redacted
	}
	return value
}

func applyFile(cfg *Config, path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected key = value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = trimValue(value)
		applyKey(cfg, key, value)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func applyEnv(cfg *Config, environ []string) {
	env := map[string]string{}
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := env[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		switch key {
		case "TWITCH_USERNAME":
			cfg.Twitch.Username = value
		case "TWITCH_ACCESS_TOKEN":
			cfg.Twitch.OAuthToken = normalizeIRCOAuthToken(value)
		case "TWITCH_REFRESH_TOKEN":
			cfg.Twitch.RefreshToken = value
		case "TWITCH_CLIENT_ID":
			cfg.Twitch.ClientID = value
		case "TWITCH_CLIENT_SECRET":
			cfg.Twitch.ClientSecret = value
		case "TWITCH_REDIRECT_URL":
			cfg.Twitch.RedirectURL = value
		case "TWI_TWITCH_USERNAME":
			cfg.Twitch.Username = value
		case "TWI_TWITCH_OAUTH_TOKEN":
			cfg.Twitch.OAuthToken = value
		case "TWI_TWITCH_REFRESH_TOKEN":
			cfg.Twitch.RefreshToken = value
		case "TWI_TWITCH_CLIENT_ID":
			cfg.Twitch.ClientID = value
		case "TWI_TWITCH_CLIENT_SECRET":
			cfg.Twitch.ClientSecret = value
		case "TWI_TWITCH_REDIRECT_URL":
			cfg.Twitch.RedirectURL = value
		case "TWI_DEFAULT_CHANNELS":
			cfg.DefaultChannels = splitList(value)
		case "TWI_ENABLE_KITTY_IMAGES":
			cfg.Features.EnableKittyImages = parseBool(value, cfg.Features.EnableKittyImages)
		case "TWI_ENABLE_MOUSE":
			cfg.Features.EnableMouse = parseBool(value, cfg.Features.EnableMouse)
		case "TWI_IMAGE_MODE":
			cfg.Features.ImageMode = value
		case "TWI_AVATAR_MODE":
			cfg.Features.AvatarMode = value
		case "TWI_EMOJI_MODE":
			cfg.Features.EmojiMode = value
		case "TWI_EMOJI_PROVIDER":
			cfg.Features.EmojiProvider = value
		case "TWI_EMOJI_URL_TEMPLATE":
			cfg.Features.EmojiURLTemplate = value
		case "TWI_EMOTE_MODE":
			cfg.Features.EmoteMode = value
		case "TWI_ANIMATION_MODE":
			cfg.Features.AnimationMode = value
		case "TWI_THEME_NAME":
			cfg.Features.ThemeName = value
		case "TWI_THEME_BACKGROUND":
			cfg.Features.ThemeCustom.Background = value
		case "TWI_THEME_FOREGROUND":
			cfg.Features.ThemeCustom.Foreground = value
		case "TWI_THEME_ACCENT":
			cfg.Features.ThemeCustom.Accent = value
		case "TWI_THEME_MUTED":
			cfg.Features.ThemeCustom.Muted = value
		case "TWI_THEME_BORDER":
			cfg.Features.ThemeCustom.Border = value
		case "TWI_THEME_SURFACE":
			cfg.Features.ThemeCustom.Surface = value
		case "TWI_THEME_WARNING":
			cfg.Features.ThemeCustom.Warning = value
		case "TWI_THEME_ERROR":
			cfg.Features.ThemeCustom.Error = value
		case "TWI_THEME_SUCCESS":
			cfg.Features.ThemeCustom.Success = value
		case "TWI_STREAM_STATUS_MODE":
			cfg.Features.StreamStatusMode = value
		case "TWI_EMOTE_AUTOCOMPLETE_MODE":
			cfg.Features.EmoteAutocompleteMode = value
		case "TWI_DEBUG_LOG":
			cfg.Debug.Enabled = parseBool(value, cfg.Debug.Enabled)
		case "TWI_DEBUG_LOG_PATH":
			cfg.Debug.LogPath = value
		}
	}
}

func applyOverrides(cfg *Config, overrides Overrides) {
	if overrides.DebugLogSet {
		cfg.Debug.Enabled = overrides.DebugLogEnabled
	}
	if strings.TrimSpace(overrides.DebugLogPath) != "" {
		cfg.Debug.LogPath = overrides.DebugLogPath
	}
}

func applyKey(cfg *Config, key, value string) {
	switch key {
	case "twitch_username":
		cfg.Twitch.Username = value
	case "twitch_oauth_token":
		cfg.Twitch.OAuthToken = value
	case "twitch_refresh_token":
		cfg.Twitch.RefreshToken = value
	case "twitch_client_id":
		cfg.Twitch.ClientID = value
	case "twitch_client_secret":
		cfg.Twitch.ClientSecret = value
	case "twitch_redirect_url":
		cfg.Twitch.RedirectURL = value
	case "default_channels":
		cfg.DefaultChannels = splitList(value)
	case "enable_kitty_images":
		cfg.Features.EnableKittyImages = parseBool(value, cfg.Features.EnableKittyImages)
	case "enable_mouse":
		cfg.Features.EnableMouse = parseBool(value, cfg.Features.EnableMouse)
	case "image_mode":
		cfg.Features.ImageMode = value
	case "avatar_mode":
		cfg.Features.AvatarMode = value
	case "emoji_mode":
		cfg.Features.EmojiMode = value
	case "emoji_provider":
		cfg.Features.EmojiProvider = value
	case "emoji_url_template":
		cfg.Features.EmojiURLTemplate = value
	case "emote_mode":
		cfg.Features.EmoteMode = value
	case "animation_mode":
		cfg.Features.AnimationMode = value
	case "theme_name":
		cfg.Features.ThemeName = value
	case "theme_background":
		cfg.Features.ThemeCustom.Background = value
	case "theme_foreground":
		cfg.Features.ThemeCustom.Foreground = value
	case "theme_accent":
		cfg.Features.ThemeCustom.Accent = value
	case "theme_muted":
		cfg.Features.ThemeCustom.Muted = value
	case "theme_border":
		cfg.Features.ThemeCustom.Border = value
	case "theme_surface":
		cfg.Features.ThemeCustom.Surface = value
	case "theme_warning":
		cfg.Features.ThemeCustom.Warning = value
	case "theme_error":
		cfg.Features.ThemeCustom.Error = value
	case "theme_success":
		cfg.Features.ThemeCustom.Success = value
	case "stream_status_mode":
		cfg.Features.StreamStatusMode = value
	case "emote_autocomplete_mode":
		cfg.Features.EmoteAutocompleteMode = value
	case "debug_logging":
		cfg.Debug.Enabled = parseBool(value, cfg.Debug.Enabled)
	case "debug_log_path":
		cfg.Debug.LogPath = value
	}
}

func normalizeIRCOAuthToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(strings.ToLower(value), "oauth:") {
		return value
	}
	return "oauth:" + value
}

func trimValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.Trim(value, `'`)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
		value = strings.ReplaceAll(value, `"`, "")
		value = strings.ReplaceAll(value, `'`, "")
	}
	return value
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	return normalizeChannels(parts)
}

func normalizeChannels(values []string) []string {
	channels := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
		if value != "" {
			channels = append(channels, value)
		}
	}
	return channels
}

func parseBool(value string, fallback bool) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func quote(value string) string {
	return strconv.Quote(value)
}

func containsSecretMarker(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"oauth:",
		"oauth_token=",
		"oauth-token=",
		"oauth_token:",
		"oauth-token:",
		"access_token=",
		"access-token=",
		"access_token:",
		"access-token:",
		"refresh_token=",
		"refresh-token=",
		"refresh_token:",
		"refresh-token:",
		"client_secret=",
		"client-secret=",
		"client_secret:",
		"client-secret:",
		"authorization_code=",
		"authorization-code=",
		"authorization_code:",
		"authorization-code:",
		"code_verifier=",
		"code-verifier=",
		"code_verifier:",
		"code-verifier:",
		"code_challenge=",
		"code-challenge=",
		"code_challenge:",
		"code-challenge:",
		"state=",
		"state:",
		"code=",
		"code:",
		"authorization=",
		"authorization: bearer",
		"bearer ",
		"bearer%20",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsURLUserInfo(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return parsed.User != nil
}

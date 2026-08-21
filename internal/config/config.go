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
	EnableMouse           bool
	AvatarMode            string
	AnimationMode         string
	ThemeName             string
	ThemeCustom           theme.Palette
	StreamStatusMode      string
	EmoteAutocompleteMode string
	// MessageLayout selects the chat message arrangement: "inline",
	// "grouped", or "compact". See internal/render.LayoutMode.
	MessageLayout string
	// BadgeMode selects how badges render beside a username: "glyph",
	// "text", or "off". See internal/render.BadgeMode.
	BadgeMode string
	// HighlightEmotes draws emotes and emoji on a tinted chip background.
	HighlightEmotes bool
	// FullUsername appends the login when it differs from the display name.
	FullUsername bool
	// SidebarWidth and ActivityWidth override the responsive default width
	// of the channel sidebar and the activity column, in terminal cells.
	// Zero means "size automatically from the terminal width", which is the
	// default and what most terminals want. Values are clamped to a usable
	// range rather than rejected, so a config that asks for something the
	// terminal cannot afford degrades instead of failing.
	SidebarWidth  int
	ActivityWidth int
	// ScrollbackLimit caps how many messages each channel retains. Chat is
	// unbounded upstream, and every retained message is re-rendered on each
	// repaint, so an uncapped buffer degrades the frame time of a long
	// session until the UI cannot keep up with the animation tick. Zero or
	// negative disables trimming and restores the old unbounded behavior.
	ScrollbackLimit int
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

// DefaultScrollbackLimit is the per-channel message cap applied when no
// scrollback_limit is configured. It is high enough that a viewer scrolling
// back through a busy stream keeps well more history than fits on screen, and
// low enough that a full repaint stays inside the animation frame budget.
const DefaultScrollbackLimit = 2000

func Default() Config {
	return Config{
		Features: FeatureConfig{
			EnableMouse:           true,
			AvatarMode:            "initials",
			AnimationMode:         "fast",
			ThemeName:             "claude",
			StreamStatusMode:      "auto",
			EmoteAutocompleteMode: "auto",
			MessageLayout:         "inline",
			BadgeMode:             "glyph",
			HighlightEmotes:       true,
			FullUsername:          false,
			ScrollbackLimit:       DefaultScrollbackLimit,
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
	// The values and their order both come from the settings table, so a
	// setting cannot end up in one and be missing from the other -- which,
	// with the two written out separately, silently dropped it from the
	// file.
	order := make([]string, 0, len(settings))
	updates := make(map[string]string, len(settings))
	for _, s := range settings {
		if !s.persisted {
			continue
		}
		order = append(order, s.key)
		updates[s.key] = s.format(cfg)
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

// RedactedString renders the effective configuration for `twi config show`,
// with credentials replaced by a redaction marker.
//
// It reports every setting in the table, so a setting cannot be added and
// then be quietly missing from the one command whose job is to say what the
// configuration currently is. Seven settings used to be missing exactly that
// way.
func (c Config) RedactedString() string {
	lines := make([]string, 0, len(settings)+1)
	lines = append(lines, "path = "+quote(RedactDisplayValue(c.Path)))
	for _, s := range settings {
		lines = append(lines, s.key+" = "+s.displayValue(c))
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

// legacyEnvAliases are the environment variables twi honoured before the
// TWI_ prefix existed. They are kept working, but are listed apart from the
// settings table because they are not a naming pattern to extend -- and
// because TWITCH_ACCESS_TOKEN accepts a token without the "oauth:" prefix
// that Twitch IRC requires, and adds it.
var legacyEnvAliases = map[string]func(cfg *Config, value string){
	"TWITCH_USERNAME":      func(cfg *Config, v string) { cfg.Twitch.Username = v },
	"TWITCH_ACCESS_TOKEN":  func(cfg *Config, v string) { cfg.Twitch.OAuthToken = normalizeIRCOAuthToken(v) },
	"TWITCH_REFRESH_TOKEN": func(cfg *Config, v string) { cfg.Twitch.RefreshToken = v },
	"TWITCH_CLIENT_ID":     func(cfg *Config, v string) { cfg.Twitch.ClientID = v },
	"TWITCH_CLIENT_SECRET": func(cfg *Config, v string) { cfg.Twitch.ClientSecret = v },
	"TWITCH_REDIRECT_URL":  func(cfg *Config, v string) { cfg.Twitch.RedirectURL = v },
}

// applyEnv applies every recognized environment variable to cfg.
//
// The variables are applied in sorted order, which is what decides the winner
// when a setting is given both ways: "TWITCH_USERNAME" sorts before
// "TWI_TWITCH_USERNAME", so the modern TWI_-prefixed name is applied second
// and wins. Blank values are skipped rather than clearing a configured value.
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
		if apply, ok := legacyEnvAliases[key]; ok {
			apply(cfg, value)
			continue
		}
		if s, ok := settingsByEnv[key]; ok {
			s.apply(cfg, value)
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

// applyKey applies one `key = value` line from a config file.
// An unrecognized key is ignored, so a config written by a newer twi still
// loads in an older one.
func applyKey(cfg *Config, key, value string) {
	if s, ok := settingsByKey[key]; ok {
		s.apply(cfg, value)
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

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
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

// containsSecretMarker reports whether a config value looks like it carries a
// credential, so `twi config show` prints a marker instead of the value.
//
// See the note on debuglog.containsCredentialMarker: twi has three scanners of
// this shape and they must stay separate. This one reads config values, where
// a credential arrives as an explicit "key=" or "key:" pair, so it matches
// those forms and their hyphenated spellings rather than bare words -- a bare
// "authorization" would redact a legitimate setting.
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

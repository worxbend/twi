package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const redacted = "[redacted]"

// Config is the effective application configuration after merging defaults,
// config file values, environment variables, and CLI overrides.
type Config struct {
	Path            string
	Twitch          TwitchConfig
	DefaultChannels []string
	Features        FeatureConfig
}

type TwitchConfig struct {
	Username     string
	OAuthToken   string
	RefreshToken string
	ClientID     string
	ClientSecret string
}

type FeatureConfig struct {
	EnableKittyImages bool
	EnableMouse       bool
	ImageMode         string
	AvatarMode        string
	EmojiMode         string
	EmoteMode         string
	AnimationMode     string
}

type Overrides struct {
	ConfigPath string
	Channels   []string
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

	return cfg, nil
}

func Default() Config {
	return Config{
		Features: FeatureConfig{
			EnableKittyImages: true,
			EnableMouse:       true,
			ImageMode:         "auto",
			AvatarMode:        "initials",
			EmojiMode:         "unicode",
			EmoteMode:         "text",
			AnimationMode:     "fast",
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

func (c Config) RedactedString() string {
	lines := []string{
		"path = " + quote(c.Path),
		"twitch_username = " + quote(c.Twitch.Username),
		"twitch_oauth_token = " + quote(redact(c.Twitch.OAuthToken)),
		"twitch_refresh_token = " + quote(redact(c.Twitch.RefreshToken)),
		"twitch_client_id = " + quote(c.Twitch.ClientID),
		"twitch_client_secret = " + quote(redact(c.Twitch.ClientSecret)),
		"default_channels = " + quote(strings.Join(c.DefaultChannels, ",")),
		"enable_kitty_images = " + strconv.FormatBool(c.Features.EnableKittyImages),
		"enable_mouse = " + strconv.FormatBool(c.Features.EnableMouse),
		"image_mode = " + quote(c.Features.ImageMode),
		"avatar_mode = " + quote(c.Features.AvatarMode),
		"emoji_mode = " + quote(c.Features.EmojiMode),
		"emote_mode = " + quote(c.Features.EmoteMode),
		"animation_mode = " + quote(c.Features.AnimationMode),
	}
	return strings.Join(lines, "\n") + "\n"
}

func redact(value string) string {
	if value == "" {
		return ""
	}
	return redacted
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
		switch key {
		case "TWITCH_USERNAME":
			cfg.Twitch.Username = env[key]
		case "TWITCH_ACCESS_TOKEN":
			cfg.Twitch.OAuthToken = normalizeIRCOAuthToken(env[key])
		case "TWITCH_REFRESH_TOKEN":
			cfg.Twitch.RefreshToken = env[key]
		case "TWITCH_CLIENT_ID":
			cfg.Twitch.ClientID = env[key]
		case "TWITCH_CLIENT_SECRET":
			cfg.Twitch.ClientSecret = env[key]
		case "TWI_TWITCH_USERNAME":
			cfg.Twitch.Username = env[key]
		case "TWI_TWITCH_OAUTH_TOKEN":
			cfg.Twitch.OAuthToken = env[key]
		case "TWI_TWITCH_REFRESH_TOKEN":
			cfg.Twitch.RefreshToken = env[key]
		case "TWI_TWITCH_CLIENT_ID":
			cfg.Twitch.ClientID = env[key]
		case "TWI_TWITCH_CLIENT_SECRET":
			cfg.Twitch.ClientSecret = env[key]
		case "TWI_DEFAULT_CHANNELS":
			cfg.DefaultChannels = splitList(env[key])
		case "TWI_ENABLE_KITTY_IMAGES":
			cfg.Features.EnableKittyImages = parseBool(env[key], cfg.Features.EnableKittyImages)
		case "TWI_ENABLE_MOUSE":
			cfg.Features.EnableMouse = parseBool(env[key], cfg.Features.EnableMouse)
		case "TWI_IMAGE_MODE":
			cfg.Features.ImageMode = env[key]
		case "TWI_AVATAR_MODE":
			cfg.Features.AvatarMode = env[key]
		case "TWI_EMOJI_MODE":
			cfg.Features.EmojiMode = env[key]
		case "TWI_EMOTE_MODE":
			cfg.Features.EmoteMode = env[key]
		case "TWI_ANIMATION_MODE":
			cfg.Features.AnimationMode = env[key]
		}
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
	case "emote_mode":
		cfg.Features.EmoteMode = value
	case "animation_mode":
		cfg.Features.AnimationMode = value
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

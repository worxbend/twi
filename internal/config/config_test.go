package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := strings.Join([]string{
		`twitch_username = "file_user"`,
		`twitch_oauth_token = "file_token"`,
		`default_channels = "fileone,filetwo"`,
		`animation_mode = "reduced"`,
		`enable_mouse = "false"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{
		"TWI_TWITCH_USERNAME=env_user",
		"TWI_DEFAULT_CHANNELS=envone,envtwo",
	}, Overrides{
		ConfigPath: path,
		Channels:   []string{"cli"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Twitch.Username != "env_user" {
		t.Fatalf("username = %q, want env_user", cfg.Twitch.Username)
	}
	if cfg.Twitch.OAuthToken != "file_token" {
		t.Fatalf("token = %q, want file_token", cfg.Twitch.OAuthToken)
	}
	if !reflect.DeepEqual(cfg.DefaultChannels, []string{"cli"}) {
		t.Fatalf("channels = %#v, want cli override", cfg.DefaultChannels)
	}
	if cfg.Features.AnimationMode != "reduced" {
		t.Fatalf("animation mode = %q, want reduced", cfg.Features.AnimationMode)
	}
	if cfg.Features.EnableMouse {
		t.Fatal("enable mouse = true, want file value false")
	}
}

func TestLoadMouseFeatureFromEnvAndConfigShow(t *testing.T) {
	cfg, err := LoadEnvOnly([]string{"TWI_ENABLE_MOUSE=false"}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Features.EnableMouse {
		t.Fatal("EnableMouse = true, want false from env")
	}
	if !strings.Contains(cfg.RedactedString(), "enable_mouse = false") {
		t.Fatalf("redacted config missing mouse flag:\n%s", cfg.RedactedString())
	}
}

func TestLoadDebugLoggingFromFileEnvAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := strings.Join([]string{
		`debug_logging = false`,
		`debug_log_path = "/tmp/file-debug.log"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{
		"TWI_DEBUG_LOG=true",
		"TWI_DEBUG_LOG_PATH=/tmp/env-debug.log",
	}, Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug.Enabled {
		t.Fatal("Debug.Enabled = false, want env override true")
	}
	if cfg.Debug.LogPath != "/tmp/env-debug.log" {
		t.Fatalf("Debug.LogPath = %q, want env path", cfg.Debug.LogPath)
	}

	cfg, err = Load([]string{
		"TWI_DEBUG_LOG=true",
		"TWI_DEBUG_LOG_PATH=/tmp/env-debug.log",
	}, Overrides{
		ConfigPath:      path,
		DebugLogSet:     true,
		DebugLogEnabled: false,
		DebugLogPath:    "/tmp/cli-debug.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Debug.Enabled {
		t.Fatal("Debug.Enabled = true, want CLI override false")
	}
	if cfg.Debug.LogPath != "/tmp/cli-debug.log" {
		t.Fatalf("Debug.LogPath = %q, want CLI path", cfg.Debug.LogPath)
	}
}

func TestLoadEmojiProviderConfigAndRedactsUnsafeTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := strings.Join([]string{
		`emoji_provider = "custom"`,
		`emoji_url_template = "https://emoji.example/assets/{id}.png"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{
		"TWI_EMOJI_PROVIDER=twemoji",
		"TWI_EMOJI_URL_TEMPLATE=https://cdn.example/{id}.png?access_token=secret",
	}, Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Features.EmojiProvider != "twemoji" {
		t.Fatalf("EmojiProvider = %q, want env override twemoji", cfg.Features.EmojiProvider)
	}
	if cfg.Features.EmojiURLTemplate != "https://cdn.example/{id}.png?access_token=secret" {
		t.Fatalf("EmojiURLTemplate = %q, want env override", cfg.Features.EmojiURLTemplate)
	}
	output := cfg.RedactedString()
	if !strings.Contains(output, `emoji_provider = "twemoji"`) {
		t.Fatalf("redacted config missing emoji provider:\n%s", output)
	}
	if strings.Contains(output, "access_token=secret") {
		t.Fatalf("redacted config leaked unsafe emoji URL template:\n%s", output)
	}
	if !strings.Contains(output, `emoji_url_template = "[redacted]"`) {
		t.Fatalf("redacted config did not redact unsafe emoji URL template:\n%s", output)
	}

	cfg.Features.EmojiURLTemplate = "https://user:secret@emoji.example/{id}.png"
	if output := cfg.RedactedString(); strings.Contains(output, "user:secret") || !strings.Contains(output, `emoji_url_template = "[redacted]"`) {
		t.Fatalf("redacted config did not redact URL userinfo:\n%s", output)
	}
}

func TestRedactedStringDoesNotLeakSecrets(t *testing.T) {
	cfg := Default()
	cfg.Path = filepath.Join(t.TempDir(), "config.toml?state=path-state-secret&code=path-code-secret")
	cfg.Twitch.OAuthToken = "oauth:secret"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientSecret = "client-secret"
	cfg.Features.EmojiURLTemplate = "https://cdn.example/{id}.png?client_secret=secret&authorization_code=auth-code-secret&state=state-secret&code=code-secret"

	output := cfg.RedactedString()

	for _, secret := range []string{"oauth:secret", "refresh-secret", "client-secret", "client_secret=secret", "auth-code-secret", "state-secret", "code-secret", "path-state-secret", "path-code-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output leaked %q: %s", secret, output)
		}
	}
	if strings.Count(output, redacted) != 5 {
		t.Fatalf("redacted output = %q, want five redactions", output)
	}
}

func TestRedactedStringIncludesDebugLoggingWithoutLeakingUnsafePath(t *testing.T) {
	cfg := Default()
	cfg.Debug.Enabled = true
	cfg.Debug.LogPath = filepath.Join(t.TempDir(), "debug.log?code=debug-code-secret&client_secret=debug-client-secret")

	output := cfg.RedactedString()

	for _, want := range []string{`debug_logging = true`, `debug_log_path = "[redacted]"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("redacted output missing %q:\n%s", want, output)
		}
	}
	for _, secret := range []string{"debug-code-secret", "debug-client-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output leaked %q:\n%s", secret, output)
		}
	}
}

func TestLoadAcceptsTwitchDotenvAliases(t *testing.T) {
	cfg, err := LoadEnvOnly([]string{
		"TWITCH_USERNAME=alias_user",
		"TWITCH_ACCESS_TOKEN=plain-access-token",
		"TWITCH_REFRESH_TOKEN=refresh-secret",
		"TWITCH_CLIENT_ID=client-id",
		"TWITCH_CLIENT_SECRET=client-secret",
	}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Twitch.Username != "alias_user" {
		t.Fatalf("username = %q, want alias_user", cfg.Twitch.Username)
	}
	if cfg.Twitch.OAuthToken != "oauth:plain-access-token" {
		t.Fatalf("oauth token = %q, want oauth-prefixed alias token", cfg.Twitch.OAuthToken)
	}
	if cfg.Twitch.RefreshToken != "refresh-secret" {
		t.Fatalf("refresh token = %q, want refresh-secret", cfg.Twitch.RefreshToken)
	}
	if cfg.Twitch.ClientID != "client-id" {
		t.Fatalf("client id = %q, want client-id", cfg.Twitch.ClientID)
	}
	if cfg.Twitch.ClientSecret != "client-secret" {
		t.Fatalf("client secret = %q, want client-secret", cfg.Twitch.ClientSecret)
	}
}

func TestCanonicalEnvOverridesTwitchDotenvAliases(t *testing.T) {
	cfg, err := LoadEnvOnly([]string{
		"TWITCH_USERNAME=alias_user",
		"TWITCH_ACCESS_TOKEN=alias-token",
		"TWITCH_REFRESH_TOKEN=alias-refresh",
		"TWITCH_CLIENT_ID=alias-client-id",
		"TWITCH_CLIENT_SECRET=alias-client-secret",
		"TWI_TWITCH_USERNAME=canonical_user",
		"TWI_TWITCH_OAUTH_TOKEN=oauth:canonical-token",
		"TWI_TWITCH_REFRESH_TOKEN=canonical-refresh",
		"TWI_TWITCH_CLIENT_ID=canonical-client-id",
		"TWI_TWITCH_CLIENT_SECRET=canonical-client-secret",
	}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Twitch.Username != "canonical_user" {
		t.Fatalf("username = %q, want canonical_user", cfg.Twitch.Username)
	}
	if cfg.Twitch.OAuthToken != "oauth:canonical-token" {
		t.Fatalf("oauth token = %q, want canonical token", cfg.Twitch.OAuthToken)
	}
	if cfg.Twitch.RefreshToken != "canonical-refresh" {
		t.Fatalf("refresh token = %q, want canonical-refresh", cfg.Twitch.RefreshToken)
	}
	if cfg.Twitch.ClientID != "canonical-client-id" {
		t.Fatalf("client id = %q, want canonical-client-id", cfg.Twitch.ClientID)
	}
	if cfg.Twitch.ClientSecret != "canonical-client-secret" {
		t.Fatalf("client secret = %q, want canonical-client-secret", cfg.Twitch.ClientSecret)
	}
}

func TestLoadIgnoresEmptyEnvValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := strings.Join([]string{
		`twitch_username = "file_user"`,
		`twitch_client_id = "file-client-id"`,
		`default_channels = "fileone"`,
		`animation_mode = "reduced"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{
		"TWI_TWITCH_USERNAME=",
		"TWI_TWITCH_CLIENT_ID=",
		"TWI_DEFAULT_CHANNELS=",
		"TWI_ANIMATION_MODE=",
	}, Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Twitch.Username != "file_user" {
		t.Fatalf("username = %q, want file_user", cfg.Twitch.Username)
	}
	if cfg.Twitch.ClientID != "file-client-id" {
		t.Fatalf("client ID = %q, want file-client-id", cfg.Twitch.ClientID)
	}
	if !reflect.DeepEqual(cfg.DefaultChannels, []string{"fileone"}) {
		t.Fatalf("channels = %#v, want fileone", cfg.DefaultChannels)
	}
	if cfg.Features.AnimationMode != "reduced" {
		t.Fatalf("animation mode = %q, want reduced", cfg.Features.AnimationMode)
	}
}

func TestLoadEnvOnlySkipsConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("not a key value line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadEnvOnly([]string{"TWI_TWITCH_USERNAME=env_user"}, Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path {
		t.Fatalf("path = %q, want %q", cfg.Path, path)
	}
	if cfg.Twitch.Username != "env_user" {
		t.Fatalf("username = %q, want env_user", cfg.Twitch.Username)
	}
}

func TestWriteNonSecretFileCreatesConfigWithoutSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twi", "config.toml")
	cfg := Default()
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:token-private"
	cfg.Twitch.RefreshToken = "refresh-private"
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.ClientSecret = "client-private"
	cfg.DefaultChannels = []string{"one", "#two"}
	cfg.Features.EnableKittyImages = false
	cfg.Features.EnableMouse = false
	cfg.Features.ImageMode = "normal"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmojiProvider = "custom"
	cfg.Features.EmoteMode = "image"
	cfg.Features.AnimationMode = "reduced"

	if err := WriteNonSecretFile(path, cfg); err != nil {
		t.Fatalf("WriteNonSecretFile returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		`twitch_username = "viewer"`,
		`twitch_client_id = "client-id"`,
		`default_channels = "one,two"`,
		`enable_kitty_images = false`,
		`enable_mouse = false`,
		`image_mode = "normal"`,
		`avatar_mode = "image"`,
		`emoji_mode = "image"`,
		`emoji_provider = "custom"`,
		`emote_mode = "image"`,
		`animation_mode = "reduced"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("written config missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"oauth:token-private", "refresh-private", "client-private", "twitch_oauth_token", "twitch_refresh_token", "twitch_client_secret"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("written config leaked %q:\n%s", forbidden, output)
		}
	}
}

func TestWriteNonSecretFileUpdatesAllowedKeysAndPreservesExistingSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := strings.Join([]string{
		"# keep this comment",
		`twitch_username = "old_viewer"`,
		`twitch_oauth_token = "oauth:existing-private"`,
		`twitch_refresh_token = "refresh-existing-private"`,
		`twitch_client_secret = "client-existing-private"`,
		`unknown_key = "kept"`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	cfg.Twitch.Username = "new_viewer"
	cfg.Twitch.OAuthToken = "oauth:new-private"
	cfg.Twitch.RefreshToken = "refresh-new-private"
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.ClientSecret = "client-new-private"
	cfg.DefaultChannels = []string{"newchan"}
	cfg.Features.EnableMouse = false

	if err := WriteNonSecretFile(path, cfg); err != nil {
		t.Fatalf("WriteNonSecretFile returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		"# keep this comment",
		`twitch_username = "new_viewer"`,
		`twitch_oauth_token = "oauth:existing-private"`,
		`twitch_refresh_token = "refresh-existing-private"`,
		`twitch_client_secret = "client-existing-private"`,
		`unknown_key = "kept"`,
		`twitch_client_id = "client-id"`,
		`default_channels = "newchan"`,
		`emoji_provider = "twemoji"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("updated config missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"oauth:new-private", "refresh-new-private", "client-new-private"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("updated config wrote new secret %q:\n%s", forbidden, output)
		}
	}
}

func TestDefaultCacheDirUsesPlatformCacheDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_CACHE_HOME does not define UserCacheDir on Windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	got, err := DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir returned error: %v", err)
	}
	want := filepath.Join(dir, "twi")
	if got != want {
		t.Fatalf("DefaultCacheDir = %q, want %q", got, want)
	}
}

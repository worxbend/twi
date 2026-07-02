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

func TestRedactedStringDoesNotLeakSecrets(t *testing.T) {
	cfg := Default()
	cfg.Twitch.OAuthToken = "oauth:secret"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientSecret = "client-secret"

	output := cfg.RedactedString()

	for _, secret := range []string{"oauth:secret", "refresh-secret", "client-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output leaked %q: %s", secret, output)
		}
	}
	if strings.Count(output, redacted) != 3 {
		t.Fatalf("redacted output = %q, want three redactions", output)
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

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

func TestDoctorRunsWithoutCredentialsAndUsesWarnings(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=dumb"},
		CacheDir: cacheDir,
		ReachabilityProbe: func(context.Context) error {
			return errors.New("network unavailable")
		},
	})

	for _, name := range []string{"config file", "twitch username", "oauth token", "token validation", "twitch reachability", "terminal", "kitty graphics"} {
		check := doctorCheck(t, report, name)
		if check.Status != DoctorStatusWarn {
			t.Fatalf("%s status = %q, want warn; detail=%q", name, check.Status, check.Detail)
		}
	}
	if check := doctorCheck(t, report, "cache directory"); check.Status != DoctorStatusOK {
		t.Fatalf("cache status = %q, want ok; detail=%q", check.Status, check.Detail)
	}
	if check := doctorCheck(t, report, "asset cache pruning"); check.Status != DoctorStatusOK {
		t.Fatalf("cache pruning status = %q, want ok; detail=%q", check.Status, check.Detail)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache diagnostics left entries behind: %#v", entries)
	}
}

func TestDoctorReportsCredentialPresenceAndValidationWithoutSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.ClientSecret = "client-secret"

	validator := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Result: twitch.TokenValidationResult{
			Status:        twitch.TokenValidationMissingScope,
			Identity:      twitch.TokenIdentity{UserID: "42", Login: "viewer", DisplayName: "Viewer"},
			Scopes:        []twitch.TokenScope{twitch.ScopeChatRead},
			MissingScopes: []twitch.TokenScope{twitch.ScopeChatEdit},
		},
	})

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=xterm-256color", "COLORTERM=truecolor", "KITTY_WINDOW_ID=1"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error {
			return nil
		},
		TokenValidator: validator,
	})

	for _, name := range []string{"twitch username", "oauth token", "refresh token", "client id", "client secret"} {
		check := doctorCheck(t, report, name)
		if check.Status != DoctorStatusOK || check.Detail != "present" {
			t.Fatalf("%s = (%q, %q), want ok present", name, check.Status, check.Detail)
		}
	}
	validation := doctorCheck(t, report, "token validation")
	if validation.Status != DoctorStatusWarn || !strings.Contains(validation.Detail, "chat:edit") {
		t.Fatalf("token validation = (%q, %q), want missing chat:edit warning", validation.Status, validation.Detail)
	}
	requests := validator.Requests()
	if len(requests) != 1 {
		t.Fatalf("validator requests = %d, want 1", len(requests))
	}
	if requests[0].Username != "viewer" ||
		requests[0].OAuthToken != "oauth:secret-token" ||
		requests[0].RefreshToken != "refresh-secret" ||
		requests[0].ClientID != "client-id" ||
		requests[0].ClientSecret != "client-secret" {
		t.Fatalf("validator request = %#v, want config Twitch credentials", requests[0])
	}
	assertDoctorDoesNotLeak(t, report, "oauth:secret-token", "refresh-secret", "client-secret")
}

func TestDoctorReportsMultipleChannelsAsConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultChannels = []string{"alpha", "beta"}

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=xterm-256color"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	})

	check := doctorCheck(t, report, "channels")
	if check.Status != DoctorStatusOK || !strings.Contains(check.Detail, "2 configured") {
		t.Fatalf("channels check = (%q, %q), want ok 2 configured", check.Status, check.Detail)
	}
}

func TestDoctorReportsTokenValidationStates(t *testing.T) {
	expiresAt := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		result     twitch.TokenValidationResult
		wantDetail string
	}{
		{
			name:       "malformed",
			result:     twitch.TokenValidationResult{Status: twitch.TokenValidationMalformed},
			wantDetail: "malformed",
		},
		{
			name: "expired with refresh",
			result: twitch.TokenValidationResult{
				Status:           twitch.TokenValidationExpired,
				RefreshAvailable: true,
				ExpiresAt:        expiresAt,
			},
			wantDetail: "refresh credentials are available",
		},
		{
			name: "wrong user",
			result: twitch.TokenValidationResult{
				Status:    twitch.TokenValidationWrongUser,
				Identity:  twitch.TokenIdentity{UserID: "42", Login: "other_viewer"},
				Scopes:    twitch.RequiredIRCScopes(),
				ExpiresAt: expiresAt,
			},
			wantDetail: "other_viewer",
		},
		{
			name: "valid wrong user fallback",
			result: twitch.TokenValidationResult{
				Status:    twitch.TokenValidationValid,
				Identity:  twitch.TokenIdentity{Login: "other_viewer"},
				Scopes:    twitch.RequiredIRCScopes(),
				ExpiresAt: expiresAt,
			},
			wantDetail: "configured username",
		},
		{
			name: "valid",
			result: twitch.TokenValidationResult{
				Status:           twitch.TokenValidationValid,
				Identity:         twitch.TokenIdentity{UserID: "42", Login: "viewer", DisplayName: "Viewer"},
				Scopes:           twitch.RequiredIRCScopes(),
				ExpiresAt:        expiresAt,
				RefreshAvailable: true,
			},
			wantDetail: "required scopes present: chat:read, chat:edit",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
			cfg.Twitch.Username = "viewer"
			cfg.Twitch.OAuthToken = "oauth:secret-token"

			report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
				Environ:           []string{"TERM=xterm-256color"},
				CacheDir:          filepath.Join(t.TempDir(), "cache"),
				ReachabilityProbe: func(context.Context) error { return nil },
				TokenValidator: twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
					Result: tt.result,
				}),
			})

			validation := doctorCheck(t, report, "token validation")
			if !strings.Contains(validation.Detail, tt.wantDetail) {
				t.Fatalf("token validation detail = %q, want it to contain %q", validation.Detail, tt.wantDetail)
			}
		})
	}
}

func TestDoctorReportsTokenValidationContext(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.ClientSecret = "client-secret"
	expiresAt := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error { return nil },
		TokenValidator: twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
			Result: twitch.TokenValidationResult{
				Status:           twitch.TokenValidationMissingScope,
				Identity:         twitch.TokenIdentity{UserID: "42", Login: "viewer"},
				Scopes:           []twitch.TokenScope{twitch.ScopeChatRead},
				MissingScopes:    []twitch.TokenScope{twitch.ScopeChatEdit},
				ExpiresAt:        expiresAt,
				RefreshAvailable: true,
			},
		}),
	})

	detail := doctorCheck(t, report, "token validation").Detail
	for _, want := range []string{
		"missing required scopes: chat:edit",
		"identity viewer (id 42)",
		"granted scopes: chat:read",
		"expires at 2026-07-02T12:30:00Z",
		"refresh credentials are available",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("token validation detail = %q, want it to contain %q", detail, want)
		}
	}
	assertDoctorDoesNotLeak(t, report, "oauth:secret-token", "secret-token", "refresh-secret", "client-secret")
}

func TestDoctorRedactsValidatorErrors(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientSecret = "client-secret"

	validator := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Err: errors.New("Bearer bearer-secret rejected with client-secret and authorization_code=auth-code-secret"),
	})

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=xterm-256color"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error {
			return nil
		},
		TokenValidator: validator,
	})

	validation := doctorCheck(t, report, "token validation")
	if validation.Status != DoctorStatusWarn {
		t.Fatalf("token validation status = %q, want warn", validation.Status)
	}
	if !strings.Contains(validation.Detail, "[redacted]") {
		t.Fatalf("token validation detail = %q, want redaction marker", validation.Detail)
	}
	assertDoctorDoesNotLeak(t, report, "oauth:secret-token", "refresh-secret", "client-secret")
	assertDoctorDoesNotLeak(t, report, "secret-token", "bearer-secret", "auth-code-secret")
}

func TestDoctorContinuesWhenValidationCanceled(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report := DoctorWithOptions(ctx, cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error { return nil },
		TokenValidator: twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
			Result: twitch.TokenValidationResult{Status: twitch.TokenValidationValid},
		}),
	})

	validation := doctorCheck(t, report, "token validation")
	if validation.Status != DoctorStatusWarn || !strings.Contains(validation.Detail, "canceled") {
		t.Fatalf("token validation = (%q, %q), want canceled warning", validation.Status, validation.Detail)
	}
	if check := doctorCheck(t, report, "cache directory"); check.Status != DoctorStatusOK {
		t.Fatalf("cache status = %q, want ok; detail=%q", check.Status, check.Detail)
	}
}

func TestDoctorReportsAssetCachePruning(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	assetCache := storage.NewDiskAssetCache(filepath.Join(cacheDir, "assets"))
	expired := storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "avatar", ID: "expired"},
		Path:      writeDoctorAssetFixture(t, "old"),
		FetchedAt: time.Now().Add(-2 * storage.DefaultAssetCacheMaxAge),
	}
	if err := assetCache.PutAsset(context.Background(), expired); err != nil {
		t.Fatalf("PutAsset fixture returned error: %v", err)
	}

	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          cacheDir,
		ReachabilityProbe: func(context.Context) error { return nil },
	})

	check := doctorCheck(t, report, "asset cache pruning")
	if check.Status != DoctorStatusOK {
		t.Fatalf("asset cache pruning status = %q, want ok; detail=%q", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "pruned=1") || !strings.Contains(check.Detail, "expired=1") {
		t.Fatalf("asset cache pruning detail = %q, want expired prune counts", check.Detail)
	}
	if _, ok, err := assetCache.GetAsset(context.Background(), expired.Key); err != nil || ok {
		t.Fatalf("expired asset after doctor ok=%v err=%v, want miss nil", ok, err)
	}
}

func TestDoctorReportsAssetCachePruningWarningsWithoutSecrets(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "access_token=secret-token")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("MkdirAll fixture returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "assets"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}

	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          cacheDir,
		ReachabilityProbe: func(context.Context) error { return nil },
	})

	check := doctorCheck(t, report, "asset cache pruning")
	if check.Status != DoctorStatusWarn {
		t.Fatalf("asset cache pruning status = %q, want warn; detail=%q", check.Status, check.Detail)
	}
	for _, want := range []string{"cleanup failed", "fix cache directory permissions", "[redacted]"} {
		if !strings.Contains(check.Detail, want) {
			t.Fatalf("asset cache pruning detail = %q, want it to contain %q", check.Detail, want)
		}
	}
	assertDoctorDoesNotLeak(t, report, "access_token=secret-token", "secret-token")
}

func TestDoctorReportsImageCapabilityStates(t *testing.T) {
	imageFeatures := config.Default().Features
	imageFeatures.AvatarMode = "image"
	imageFeatures.EmojiMode = "image"
	imageFeatures.EmoteMode = "image"

	for _, tt := range []struct {
		name       string
		features   config.FeatureConfig
		environ    []string
		cacheDir   func(*testing.T) string
		wantStatus DoctorStatus
		wantDetail []string
	}{
		{
			name:       "enabled",
			features:   imageFeatures,
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheDir:   writableDoctorCacheDir,
			wantStatus: DoctorStatusOK,
			wantDetail: []string{"enabled", "avatar=image/enabled", "emoji=image/enabled", "emote=image/enabled", "cache=writable"},
		},
		{
			name: "disabled",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheDir:   writableDoctorCacheDir,
			wantStatus: DoctorStatusOK,
			wantDetail: []string{"disabled", "image=off", "disabled by image_mode=off"},
		},
		{
			name:       "unsupported auto",
			features:   imageFeatures,
			environ:    []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			cacheDir:   writableDoctorCacheDir,
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"unsupported", "terminal=no-kitty", "text fallbacks active"},
		},
		{
			name:       "degraded cache unwritable",
			features:   imageFeatures,
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			cacheDir:   unwritableDoctorCachePath,
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"degraded", "cache=unwritable", "asset cache is not writable"},
		},
		{
			name: "explicit override degraded",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "normal"
				return features
			}(),
			environ:    []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			cacheDir:   writableDoctorCacheDir,
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"degraded", "image=normal", "no Kitty/Ghostty graphics signal"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
			cfg.Features = tt.features

			report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
				Environ:           tt.environ,
				CacheDir:          tt.cacheDir(t),
				ReachabilityProbe: func(context.Context) error { return nil },
			})

			check := doctorCheck(t, report, "image capability")
			if check.Status != tt.wantStatus {
				t.Fatalf("image capability status = %q, want %q; detail=%q", check.Status, tt.wantStatus, check.Detail)
			}
			for _, want := range tt.wantDetail {
				if !strings.Contains(check.Detail, want) {
					t.Fatalf("image capability detail = %q, want it to contain %q", check.Detail, want)
				}
			}
		})
	}
}

func TestDoctorReportsImageStackStates(t *testing.T) {
	imageFeatures := config.Default().Features
	imageFeatures.ImageMode = "normal"
	imageFeatures.AvatarMode = "image"
	imageFeatures.EmojiMode = "image"
	imageFeatures.EmoteMode = "image"

	for _, tt := range []struct {
		name       string
		features   config.FeatureConfig
		twitch     config.TwitchConfig
		environ    []string
		wantStatus DoctorStatus
		wantDetail []string
	}{
		{
			name:     "enabled",
			features: imageFeatures,
			twitch: config.TwitchConfig{
				OAuthToken: "oauth:fixture-token",
				ClientID:   "fixture-client",
			},
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			wantStatus: DoctorStatusOK,
			wantDetail: []string{"enabled", "supported=avatar,badge,twitch_emote,emoji", "cache=writable"},
		},
		{
			name: "disabled",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "off"
				return features
			}(),
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			wantStatus: DoctorStatusOK,
			wantDetail: []string{"disabled", "image_mode=off", "text fallbacks active"},
		},
		{
			name: "unsupported",
			features: func() config.FeatureConfig {
				features := imageFeatures
				features.ImageMode = "auto"
				return features
			}(),
			twitch: config.TwitchConfig{
				OAuthToken: "oauth:fixture-token",
				ClientID:   "fixture-client",
			},
			environ:    []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"unsupported", "no Kitty/Ghostty", "text fallbacks active"},
		},
		{
			name: "missing dependency",
			features: func() config.FeatureConfig {
				features := config.Default().Features
				features.ImageMode = "normal"
				features.EmoteMode = "image"
				return features
			}(),
			twitch: config.TwitchConfig{
				OAuthToken: "oauth:fixture-token",
			},
			environ:    []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"},
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"degraded", "supported=twitch_emote,emoji", "TWI_TWITCH_CLIENT_ID", "fallbacks active"},
		},
		{
			name:     "degraded ready",
			features: imageFeatures,
			twitch: config.TwitchConfig{
				OAuthToken: "oauth:fixture-token",
				ClientID:   "fixture-client",
			},
			environ:    []string{"TERM=xterm-kitty", "KITTY_WINDOW_ID=42"},
			wantStatus: DoctorStatusWarn,
			wantDetail: []string{"degraded", "supported=avatar,badge,twitch_emote,emoji", "no true-color hint", "fallbacks remain available"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
			cfg.Features = tt.features
			cfg.Twitch = tt.twitch

			report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
				Environ:           tt.environ,
				CacheDir:          filepath.Join(t.TempDir(), "cache"),
				ReachabilityProbe: func(context.Context) error { return nil },
			})

			check := doctorCheck(t, report, "image stack")
			if check.Status != tt.wantStatus {
				t.Fatalf("image stack status = %q, want %q; detail=%q", check.Status, tt.wantStatus, check.Detail)
			}
			for _, want := range tt.wantDetail {
				if !strings.Contains(check.Detail, want) {
					t.Fatalf("image stack detail = %q, want it to contain %q", check.Detail, want)
				}
			}
			if strings.Contains(check.Detail, "oauth:fixture-token") {
				t.Fatalf("image stack detail leaked token: %q", check.Detail)
			}
		})
	}
}

func TestDoctorWarnsOnUnknownEmojiProviderEvenWithTemplate(t *testing.T) {
	cfg := config.Default()
	cfg.Features.EmojiProvider = "surprise"
	cfg.Features.EmojiURLTemplate = "https://emoji.example/{id}.png"
	cfg.Features.AnimationMode = "expressive"

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	})

	check := doctorCheck(t, report, "feature modes")
	if check.Status != DoctorStatusWarn {
		t.Fatalf("feature modes status = %q, want warn; detail=%q", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "emoji_provider=surprise") {
		t.Fatalf("feature modes detail = %q, want unknown emoji provider", check.Detail)
	}
	if !strings.Contains(check.Detail, "animation=expressive") {
		t.Fatalf("feature modes detail = %q, want unknown animation mode", check.Detail)
	}
}

func TestDoctorWarnsOnUnknownThemeAndStreamStatusModes(t *testing.T) {
	cfg := config.Default()
	cfg.Features.ThemeName = "not-a-theme"
	cfg.Features.StreamStatusMode = "sometimes"
	cfg.Features.EmoteAutocompleteMode = "sometimes"

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:  []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
	})

	check := doctorCheck(t, report, "feature modes")
	if check.Status != DoctorStatusWarn {
		t.Fatalf("feature modes status = %q, want warn; detail=%q", check.Status, check.Detail)
	}
	for _, want := range []string{"theme=not-a-theme", "stream_status=sometimes", "emote_autocomplete=sometimes"} {
		if !strings.Contains(check.Detail, want) {
			t.Fatalf("feature modes detail = %q, want %q", check.Detail, want)
		}
	}
}

func TestDoctorStreamStatusCheckStates(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	off := config.Default()
	off.Features.StreamStatusMode = "off"
	report := DoctorWithOptions(context.Background(), off, DoctorOptions{CacheDir: cacheDir})
	if check := doctorCheck(t, report, "stream status polling"); check.Status != DoctorStatusWarn {
		t.Fatalf("stream status check with mode off = %q, want warn; detail=%q", check.Status, check.Detail)
	}

	missingCreds := config.Default()
	report = DoctorWithOptions(context.Background(), missingCreds, DoctorOptions{CacheDir: cacheDir})
	check := doctorCheck(t, report, "stream status polling")
	if check.Status != DoctorStatusWarn {
		t.Fatalf("stream status check without credentials = %q, want warn; detail=%q", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "twitch_client_id") || !strings.Contains(check.Detail, "twitch_oauth_token") {
		t.Fatalf("stream status detail = %q, want missing client id and oauth token", check.Detail)
	}

	ready := config.Default()
	ready.Twitch.ClientID = "client-id"
	ready.Twitch.OAuthToken = "oauth:token"
	report = DoctorWithOptions(context.Background(), ready, DoctorOptions{CacheDir: cacheDir})
	if check := doctorCheck(t, report, "stream status polling"); check.Status != DoctorStatusOK {
		t.Fatalf("stream status check with credentials = %q, want ok; detail=%q", check.Status, check.Detail)
	}
}

func doctorCheck(t *testing.T, report DoctorReport, name string) DoctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor report missing check %q: %#v", name, report.Checks)
	return DoctorCheck{}
}

func writableDoctorCacheDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cache")
}

func unwritableDoctorCachePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile cache fixture returned error: %v", err)
	}
	return path
}

func assertDoctorDoesNotLeak(t *testing.T, report DoctorReport, secrets ...string) {
	t.Helper()
	for _, check := range report.Checks {
		for _, secret := range secrets {
			if strings.Contains(check.Detail, secret) {
				t.Fatalf("%s leaked %q in detail %q", check.Name, secret, check.Detail)
			}
		}
	}
}

func writeDoctorAssetFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile fixture returned error: %v", err)
	}
	return path
}

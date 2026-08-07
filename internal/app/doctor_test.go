package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
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

	// twitch username is deliberately absent: the login is derived from the
	// OAuth token, so not configuring one is not a problem to warn about.
	for _, name := range []string{"config file", "oauth token", "token validation", "twitch reachability", "terminal"} {
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
		Environ:  []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
		CacheDir: filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error {
			return nil
		},
		TokenValidator: validator,
	})

	for _, name := range []string{"oauth token", "refresh token", "client id"} {
		check := doctorCheck(t, report, name)
		if check.Status != DoctorStatusOK || check.Detail != "present" {
			t.Fatalf("%s = (%q, %q), want ok present", name, check.Status, check.Detail)
		}
	}
	// twitch username reports how the login is resolved rather than bare
	// presence, since it is derived from the token and only a fallback.
	if user := doctorCheck(t, report, "twitch username"); user.Status != DoctorStatusOK {
		t.Fatalf("twitch username = (%q, %q), want ok", user.Status, user.Detail)
	}
	// The client secret check reports refresh capability rather than bare
	// presence, because a saved refresh token without a secret cannot be
	// redeemed. See TestDoctorWarnsWhenRefreshTokenCannotBeRedeemed.
	if secret := doctorCheck(t, report, "client secret"); secret.Status != DoctorStatusOK {
		t.Fatalf("client secret = (%q, %q), want ok when a secret is configured", secret.Status, secret.Detail)
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
			name: "valid but username is stale",
			result: twitch.TokenValidationResult{
				Status:    twitch.TokenValidationValid,
				Identity:  twitch.TokenIdentity{Login: "other_viewer"},
				Scopes:    twitch.RequiredIRCScopes(),
				ExpiresAt: expiresAt,
			},
			// The token owns the identity, so a disagreeing twitch_username is
			// reported as stale config rather than as a token problem.
			wantDetail: "is stale; the token belongs to \"other_viewer\"",
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

func TestDoctorWarnsOnUnknownLayoutAndBadgeModes(t *testing.T) {
	features := config.Default().Features
	features.MessageLayout = "sideways"
	features.BadgeMode = "sparkles"

	check := featureModesCheck(features)
	if check.Status != DoctorStatusWarn {
		t.Fatalf("feature modes status = %v, want a warning for unknown modes", check.Status)
	}
	for _, want := range []string{"message_layout=sideways", "badge_mode=sparkles"} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("detail = %q, want it to name %q", check.Detail, want)
		}
	}
}

func TestDoctorAcceptsEveryValidLayoutAndBadgeMode(t *testing.T) {
	for _, layout := range []string{"inline", "grouped", "compact"} {
		for _, badges := range []string{"glyph", "text", "off"} {
			features := config.Default().Features
			features.MessageLayout = layout
			features.BadgeMode = badges
			if check := featureModesCheck(features); check.Status != DoctorStatusOK {
				t.Errorf("layout=%s badges=%s reported %v: %s", layout, badges, check.Status, check.Detail)
			}
		}
	}
}

// TestDoctorWarnsWhenRefreshTokenCannotBeRedeemed covers the setup twi's own
// documented happy path produces: `twi login` saves a refresh token and client
// ID but never a client secret, and the refresh flow requires all three. The
// access token then expires after about four hours, chat drops, and nothing
// had warned that recovery was impossible. Doctor previously called this
// "optional OAuth client-secret flow unavailable".
func TestDoctorWarnsWhenRefreshTokenCannotBeRedeemed(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientID = "client-id"
	cfg.Twitch.ClientSecret = ""

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error { return nil },
	})

	check := doctorCheck(t, report, "client secret")
	if check.Status != DoctorStatusWarn {
		t.Fatalf("client secret = (%q, %q), want a warning", check.Status, check.Detail)
	}
	for _, want := range []string{"refresh token cannot be redeemed", "disconnect", "TWI_TWITCH_CLIENT_SECRET"} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("client secret detail = %q, want it to mention %q", check.Detail, want)
		}
	}
}

// TestDoctorNamesMissingRefreshCredential keeps the warning actionable: the
// three inputs come from three different places, so "unavailable" alone does
// not tell anyone what to go and fix.
func TestDoctorNamesMissingRefreshCredential(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.RefreshToken = "refresh-secret"
	cfg.Twitch.ClientID = "client-id"

	validator := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Result: twitch.TokenValidationResult{
			Status:           twitch.TokenValidationExpired,
			RefreshAvailable: false,
		},
	})

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error { return nil },
		TokenValidator:    validator,
	})

	check := doctorCheck(t, report, "token validation")
	if !strings.Contains(check.Detail, "missing client secret") {
		t.Fatalf("token validation detail = %q, want it to name the missing client secret", check.Detail)
	}
}

// TestDoctorClientSecretStaysSoftWithoutRefreshToken keeps the warning
// proportionate: with no refresh token there is nothing to redeem, so the
// missing secret only costs the optional client-credentials flow.
func TestDoctorClientSecretStaysSoftWithoutRefreshToken(t *testing.T) {
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "missing.toml")

	report := DoctorWithOptions(context.Background(), cfg, DoctorOptions{
		Environ:           []string{"TERM=xterm-256color"},
		CacheDir:          filepath.Join(t.TempDir(), "cache"),
		ReachabilityProbe: func(context.Context) error { return nil },
	})

	check := doctorCheck(t, report, "client secret")
	if strings.Contains(check.Detail, "disconnect") {
		t.Fatalf("client secret detail = %q, want no disconnection warning without a refresh token", check.Detail)
	}
}

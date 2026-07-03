package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/app"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/debuglog"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "twi-cli-test-config-")
	if err != nil {
		panic(err)
	}
	cacheDir, err := os.MkdirTemp("", "twi-cli-test-cache-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", cacheDir); err != nil {
		panic(err)
	}
	_ = os.Setenv("TWI_DEBUG_LOG", "")
	_ = os.Setenv("TWI_DEBUG_LOG_PATH", "")
	code := m.Run()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0", code)
	}
	for _, want := range []string{"twi chat", "twi setup", "TWI_ENABLE_MOUSE", "TWI_EMOJI_PROVIDER", "TWI_EMOJI_URL_TEMPLATE", "TWI_DEBUG_LOG"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help output missing %q: %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestChatDebugLogFlagWritesRedactedMockLog(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:debug-access-secret")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "debug-refresh-secret")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "debug-client-secret")
	logPath := filepath.Join(t.TempDir(), "debug.log")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"chat",
		"--mock",
		"--channel", "example",
		"--debug-log",
		"--debug-log-path", logPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile debug log returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{`"event":"cli.debug_log.opened"`, `"event":"cli.chat.start"`, `"event":"app.start"`, `"mock":true`} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
	assertOutputDoesNotContain(t, output, "oauth:debug-access-secret", "debug-access-secret", "debug-refresh-secret", "debug-client-secret")
}

func TestDebugLogFlagCanDisableEnvLogging(t *testing.T) {
	t.Setenv("TWI_DEBUG_LOG", "true")
	logPath := filepath.Join(t.TempDir(), "debug.log")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"chat",
		"--mock",
		"--channel", "example",
		"--debug-log=false",
		"--debug-log-path", logPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("debug log stat error = %v, want not exist", err)
	}
}

func TestDebugLogRejectsExistingGroupReadableFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(logPath, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"chat",
		"--mock",
		"--channel", "example",
		"--debug-log",
		"--debug-log-path", logPath,
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "open debug log") || !strings.Contains(stderr.String(), "private user-only") {
		t.Fatalf("stderr missing private-permission failure: %q", stderr.String())
	}
}

func TestSetupHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"setup", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"twi setup", "non-secret", "--non-interactive", "--login-dry-run", "credential store"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("setup help output missing %q: %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestSetupNonInteractiveWritesConfigAndRunsLoginDryRunWithoutSecrets(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:setup-access-secret")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "setup-refresh-secret")
	path := filepath.Join(t.TempDir(), "config.toml?state=setup-path-secret")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"setup",
		"--config", path,
		"--non-interactive",
		"--username", "viewer",
		"--client-id", "client-id",
		"--channel", "one",
		"--channel", "#two",
		"--enable-kitty-images=false",
		"--enable-mouse=false",
		"--image-mode", "normal",
		"--avatar-mode", "image",
		"--emoji-mode", "image",
		"--emoji-provider", "custom",
		"--emote-mode", "image",
		"--animation-mode", "reduced",
		"--login-dry-run",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Updated non-secret config: [redacted]") || !strings.Contains(stdout.String(), "Twitch OAuth login dry run") {
		t.Fatalf("setup output missing config update or login dry run:\n%s", stdout.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	configOutput := string(data)
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
		if !strings.Contains(configOutput, want) {
			t.Fatalf("setup config missing %q:\n%s", want, configOutput)
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String()+configOutput,
		"client-secret", "setup-access-secret", "setup-refresh-secret", "setup-path-secret", "oauth:setup-access-secret", "twitch_client_secret", "twitch_oauth_token", "twitch_refresh_token")
}

func TestSetupRejectsUnsupportedAnimationMode(t *testing.T) {
	clearTwitchCredentialEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"setup",
		"--config", path,
		"--non-interactive",
		"--username", "viewer",
		"--channel", "one",
		"--animation-mode", "expressive",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "animation mode must be one of: off, reduced, fast") {
		t.Fatalf("stderr = %q, want supported animation mode list", stderr.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", err)
	}
}

func TestSetupNonInteractiveRejectsUnsupportedExistingMode(t *testing.T) {
	clearTwitchCredentialEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		`twitch_username = "viewer"`,
		`default_channels = "one"`,
		`animation_mode = "expressive"`,
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"setup",
		"--config", path,
		"--non-interactive",
		"--client-id", "client-id",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "setup config: animation mode must be one of: off, reduced, fast") {
		t.Fatalf("stderr = %q, want supported animation mode list", stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), `animation_mode = "expressive"`) {
		t.Fatalf("config was unexpectedly rewritten:\n%s", string(data))
	}
}

func TestSetupWizardPromptsAndRunsFakeLoginHandoff(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")
	path := filepath.Join(t.TempDir(), "config.toml")

	resetLoginTestHooks(t)
	oldSetupInput := setupInput
	setupInput = strings.NewReader(strings.Join([]string{
		"viewer",
		"client-id",
		"#one, two",
		"n",
		"normal",
		"image",
		"image",
		"twemoji",
		"image",
		"reduced",
		"n",
		"login",
	}, "\n") + "\n")
	t.Cleanup(func() {
		setupInput = oldSetupInput
	})

	credentialStore := storage.NewMemoryCredentialStore()
	newCredentialStore = func() (storage.CredentialStore, error) {
		return credentialStore, nil
	}
	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?client_id=client-id&state=state-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
		ExpiresAt:        time.Now().Add(time.Minute),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{
		Identity: auth.Identity{UserID: "42", Login: "viewer"},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("oauth:access-secret"),
			RefreshToken: auth.NewSecret("refresh-secret"),
			Scopes:       auth.RequiredChatScopes(),
		},
		Scopes: auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
			Code:  auth.NewSecret("callback-code"),
			State: auth.NewSecret("state-secret"),
		}}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"setup", "--config", path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{"Twitch username", "Twitch app client ID", "Default channels", "Emoji provider", "Credential setup", "Starting login handoff", "Login succeeded for Twitch user: viewer"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("setup wizard output missing %q:\n%s", want, stdout.String())
		}
	}
	requests := fakeFlow.BeginRequests()
	if len(requests) != 1 {
		t.Fatalf("begin requests = %d, want 1", len(requests))
	}
	if requests[0].ClientID != "client-id" || requests[0].ClientSecret.Reveal() != "client-secret" {
		t.Fatalf("begin request config = %#v, want setup client ID and env client secret", requests[0])
	}
	saves := credentialStore.SavedRecords()
	if len(saves) != 1 || saves[0].AccessToken.Reveal() != "oauth:access-secret" || saves[0].RefreshToken.Reveal() != "refresh-secret" {
		t.Fatalf("saved records = %#v, want fake login tokens saved", saves)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	configOutput := string(data)
	for _, want := range []string{`twitch_username = "viewer"`, `twitch_client_id = "client-id"`, `default_channels = "one,two"`, `emoji_provider = "twemoji"`} {
		if !strings.Contains(configOutput, want) {
			t.Fatalf("setup config missing %q:\n%s", want, configOutput)
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String()+configOutput,
		"client-secret", "state-secret", "callback-code", "oauth:access-secret", "access-secret", "refresh-secret", "https://auth.example")
}

func TestLoginHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"login", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"twi login", "chat:read", "chat:edit", "not printed", "credential store", "TWI_TWITCH_CLIENT_ID"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("login help output missing %q: %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestLoginDryRunExplainsFlowAndRedactsSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:access-secret")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "refresh-secret")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"login",
		"--config", t.TempDir() + "/missing.toml",
		"--redirect-uri", "http://127.0.0.1:17643/oauth/twitch/callback?state=state-secret&client_secret=client-secret&code=callback-code",
		"--dry-run",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{
		"dry run",
		"chat:read",
		"chat:edit",
		"Redirect URI: http://127.0.0.1:17643/oauth/twitch/callback?state=<redacted>&client_secret=<redacted>&code=<redacted>",
		"Client ID: present",
		"Client secret: present",
		"saved privately",
		"saved credentials",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("dry-run output missing %q: %q", want, stdout.String())
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret", "oauth:access-secret", "access-secret", "refresh-secret", "state-secret", "callback-code")
}

func TestLoginDryRunIgnoresMalformedStoredCredentials(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeRawStoredCredentialFile(t, `{"version":1,"twitch":{"access_token":"oauth:stored-secret","expires_at":"oauth:bad-time"}}`)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml", "--dry-run"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Twitch OAuth login dry run") {
		t.Fatalf("dry-run output missing heading: %q", stdout.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "oauth:stored-secret", "stored-secret", "oauth:bad-time", "bad-time")
}

func TestLoginCompletesFakeFlowSavesCredentialsWithoutPrintingTokens(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)
	credentialStore := storage.NewMemoryCredentialStore()
	newCredentialStore = func() (storage.CredentialStore, error) {
		return credentialStore, nil
	}

	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?client_id=client-id&state=state-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
		ExpiresAt:        time.Now().Add(time.Minute),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{
		Identity: auth.Identity{UserID: "42", Login: "viewer"},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("oauth:access-secret"),
			RefreshToken: auth.NewSecret("refresh-secret"),
			Scopes:       auth.RequiredChatScopes(),
		},
		Scopes: auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}

	waiter := &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
		Code:  auth.NewSecret("callback-code"),
		State: auth.NewSecret("state-secret"),
	}}
	newLoginCallbackWaiter = func(redirectURI string) (loginCallbackWaiter, error) {
		if redirectURI != defaultLoginRedirectURI {
			t.Fatalf("redirect URI = %q, want %q", redirectURI, defaultLoginRedirectURI)
		}
		return waiter, nil
	}
	var openedURL string
	openLoginBrowser = func(_ context.Context, targetURL string) error {
		openedURL = targetURL
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if openedURL != "https://auth.example/authorize?client_id=client-id&state=state-secret" {
		t.Fatalf("opened URL = %q, want fake auth URL", openedURL)
	}
	if !waiter.closed {
		t.Fatal("callback waiter was not closed")
	}
	requests := fakeFlow.BeginRequests()
	if len(requests) != 1 {
		t.Fatalf("begin requests = %d, want 1", len(requests))
	}
	if requests[0].ClientID != "client-id" || requests[0].ClientSecret.Reveal() != "client-secret" {
		t.Fatalf("begin request config = %#v, want configured client", requests[0])
	}
	saves := credentialStore.SavedRecords()
	if len(saves) != 1 {
		t.Fatalf("saved credential records = %d, want 1", len(saves))
	}
	if saves[0].Login != "viewer" || saves[0].ClientID != "client-id" {
		t.Fatalf("saved credential identity = %#v, want viewer/client-id", saves[0])
	}
	if saves[0].AccessToken.Reveal() != "oauth:access-secret" || saves[0].RefreshToken.Reveal() != "refresh-secret" {
		t.Fatal("saved credential tokens did not match login result")
	}
	for _, want := range []string{"Starting Twitch OAuth login", "chat:read", "chat:edit", "Login succeeded for Twitch user: viewer", "Refresh token: received", "Credentials saved"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("login output missing %q: %q", want, stdout.String())
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(),
		"client-secret", "state-secret", "callback-code", "oauth:access-secret", "access-secret", "refresh-secret", "https://auth.example")
}

func TestLoginDebugLogRedactsOAuthFlowSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")
	logPath := filepath.Join(t.TempDir(), "debug.log")

	resetLoginTestHooks(t)
	credentialStore := storage.NewMemoryCredentialStore()
	newCredentialStore = func() (storage.CredentialStore, error) {
		return credentialStore, nil
	}
	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?client_id=client-id&state=state-secret&client_secret=client-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
		ExpiresAt:        time.Now().Add(time.Minute),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{
		Identity: auth.Identity{UserID: "42", Login: "viewer"},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("oauth:access-secret"),
			RefreshToken: auth.NewSecret("refresh-secret"),
			Scopes:       auth.RequiredChatScopes(),
		},
		Scopes: auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
			Code:  auth.NewSecret("callback-code"),
			State: auth.NewSecret("state-secret"),
		}}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"login",
		"--config", t.TempDir() + "/missing.toml",
		"--debug-log",
		"--debug-log-path", logPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile debug log returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		`"event":"cli.login.start"`,
		`"event":"cli.login.begin_succeeded"`,
		`"event":"cli.login.callback_received"`,
		`"event":"cli.login.complete_succeeded"`,
		`"event":"cli.login.save_succeeded"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
	assertOutputDoesNotContain(t, output,
		"client-secret", "state-secret", "callback-code", "oauth:access-secret", "access-secret", "refresh-secret", "https://auth.example")
}

func TestLoginOverwritesMalformedStoredCredentialsWithoutPrintingTokens(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")
	writeRawStoredCredentialFile(t, `{"version":1,"twitch":{"access_token":"oauth:old-stored-secret","expires_at":"oauth:bad-time"}}`)

	resetLoginTestHooks(t)
	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?state=state-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{
		Identity: auth.Identity{UserID: "42", Login: "viewer"},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("new-access-secret"),
			RefreshToken: auth.NewSecret("new-refresh-secret"),
			Scopes:       auth.RequiredChatScopes(),
		},
		Scopes: auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
			Code:  auth.NewSecret("callback-code"),
			State: auth.NewSecret("state-secret"),
		}}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	store, err := storage.NewDefaultCredentialFileStore()
	if err != nil {
		t.Fatalf("NewDefaultCredentialFileStore returned error: %v", err)
	}
	record, ok, err := store.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials after login returned error: %v", err)
	}
	if !ok {
		t.Fatal("LoadCredentials after login ok = false, want saved credentials")
	}
	if record.AccessToken.Reveal() != "new-access-secret" || record.RefreshToken.Reveal() != "new-refresh-secret" {
		t.Fatalf("saved tokens = (%q, %q), want new login tokens", record.AccessToken.Reveal(), record.RefreshToken.Reveal())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(),
		"client-secret", "state-secret", "callback-code", "new-access-secret", "new-refresh-secret", "old-stored-secret", "oauth:bad-time", "https://auth.example")
}

func TestLoginCredentialSaveFailureIsRedacted(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)
	newCredentialStore = func() (storage.CredentialStore, error) {
		return saveFailCredentialStore{err: errors.New("cannot write oauth:access-secret refresh_token=refresh-secret client_secret=client-secret")}, nil
	}

	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?state=state-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{
		Identity: auth.Identity{UserID: "42", Login: "viewer"},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("oauth:access-secret"),
			RefreshToken: auth.NewSecret("refresh-secret"),
			Scopes:       auth.RequiredChatScopes(),
		},
		Scopes: auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
			Code:  auth.NewSecret("callback-code"),
			State: auth.NewSecret("state-secret"),
		}}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "save credentials:") || !strings.Contains(stderr.String(), auth.RedactedSecret) {
		t.Fatalf("stderr missing redacted save failure: %q", stderr.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret", "state-secret", "callback-code", "oauth:access-secret", "access-secret", "refresh-secret", "https://auth.example")
}

func TestLoginUnsupportedCredentialFileFallbackFailsBeforeBrowser(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)
	newCredentialStore = func() (storage.CredentialStore, error) {
		return nil, fmt.Errorf("%w: credential-file fallback is disabled on non-Unix builds; use environment variables or a private flat config file; oauth:storage-secret state=state-secret client_secret=client-secret", storage.ErrUnsupportedCredentialFilePlatform)
	}
	flowStarted := false
	newLoginFlow = func() auth.LoginFlow {
		flowStarted = true
		return auth.NewFakeLoginFlow()
	}
	browserOpened := false
	openLoginBrowser = func(context.Context, string) error {
		browserOpened = true
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if flowStarted || browserOpened {
		t.Fatalf("login started OAuth flow=%v browser=%v, want credential storage preflight to stop first", flowStarted, browserOpened)
	}
	for _, want := range []string{"prepare credential storage", "disabled on non-Unix builds", "environment variables", "private flat config file"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret", "storage-secret", "state-secret")
}

func TestLoginCancellationIsClearAndRedacted(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)

	fakeFlow := auth.NewFakeLoginFlow()
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{}, nil
	}
	newLoginContext = func(time.Duration) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, func() {}
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "start login: login canceled") {
		t.Fatalf("stderr missing cancellation: %q", stderr.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret")
}

func TestLoginUnsupportedCallbackFailsClearly(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml", "--redirect-uri", "https://example.com/callback"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"login callback unavailable", "localhost", "127.0.0.1"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret")
}

func TestLoginBrowserFailureRedactsAuthorizationURLAndState(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)

	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?state=state-secret&client_secret=client-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
	}, nil)
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return errors.New("cannot open https://auth.example/authorize?state=state-secret&client_secret=client-secret")
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "open browser:") {
		t.Fatalf("stderr missing browser error: %q", stderr.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "client-secret", "state-secret", "https://auth.example")
}

func TestLoginOAuthFailureIsRedacted(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "client-id")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	resetLoginTestHooks(t)

	fakeFlow := auth.NewFakeLoginFlow()
	fakeFlow.QueueBegin(auth.LoginChallenge{
		AuthorizationURL: auth.NewSecret("https://auth.example/authorize?state=state-secret"),
		State:            auth.NewSecret("state-secret"),
		Scopes:           auth.RequiredChatScopes(),
	}, nil)
	fakeFlow.QueueComplete(auth.LoginResult{}, errors.New("provider rejected authorization_code=callback-code state=state-secret access_token=oauth:access-secret refresh_token=refresh-secret client_secret=client-secret"))
	newLoginFlow = func() auth.LoginFlow {
		return fakeFlow
	}
	newLoginCallbackWaiter = func(string) (loginCallbackWaiter, error) {
		return &fakeLoginCallbackWaiter{callback: auth.LoginCallback{
			Code:  auth.NewSecret("callback-code"),
			State: auth.NewSecret("state-secret"),
		}}, nil
	}
	openLoginBrowser = func(context.Context, string) error {
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"login", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "complete login:") || !strings.Contains(stderr.String(), auth.RedactedSecret) {
		t.Fatalf("stderr missing redacted OAuth failure: %q", stderr.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(),
		"client-secret", "state-secret", "callback-code", "oauth:access-secret", "access-secret", "refresh-secret", "https://auth.example")
}

func TestLocalLoginCallbackWaiterReceivesCallbackAndHidesSecrets(t *testing.T) {
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/oauth/twitch/callback", freeLocalPort(t))
	waiter, err := newLocalLoginCallbackWaiter(redirectURI)
	if err != nil {
		t.Fatalf("newLocalLoginCallbackWaiter returned error: %v", err)
	}
	defer waiter.Close()

	client := &http.Client{Timeout: time.Second}

	postResp, err := client.Post(redirectURI+"?code=post-code&state=state-secret", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", postResp.StatusCode, http.StatusMethodNotAllowed)
	}
	_ = postResp.Body.Close()

	wrongPathResp, err := client.Get(strings.Replace(redirectURI, "/oauth/twitch/callback", "/wrong", 1) + "?code=wrong-code&state=state-secret")
	if err != nil {
		t.Fatalf("GET wrong path: %v", err)
	}
	if wrongPathResp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong path status = %d, want %d", wrongPathResp.StatusCode, http.StatusNotFound)
	}
	_ = wrongPathResp.Body.Close()

	firstResp, err := client.Get(redirectURI + "?code=callback-code&state=state-secret")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	firstBody, err := io.ReadAll(firstResp.Body)
	_ = firstResp.Body.Close()
	if err != nil {
		t.Fatalf("read callback response: %v", err)
	}
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want %d; body=%q", firstResp.StatusCode, http.StatusOK, string(firstBody))
	}
	assertOutputDoesNotContain(t, string(firstBody), "callback-code", "state-secret")

	duplicateResp, err := client.Get(redirectURI + "?code=duplicate-code&state=state-secret")
	if err != nil {
		t.Fatalf("GET duplicate callback: %v", err)
	}
	if duplicateResp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want %d", duplicateResp.StatusCode, http.StatusConflict)
	}
	_ = duplicateResp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	callback, err := waiter.Wait(ctx, auth.NewSecret("state-secret"))
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if callback.Code.Reveal() != "callback-code" || callback.State.Reveal() != "state-secret" || callback.ExpectedState.Reveal() != "state-secret" {
		t.Fatalf("callback = %#v, want received code/state with expected state", callback)
	}
}

func TestMockChat(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"chat", "--mock", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#example") {
		t.Fatalf("mock chat output missing channel: %q", stdout.String())
	}
}

func TestMockChatIgnoresMalformedStoredCredentials(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeRawStoredCredentialFile(t, `{"version":1,"twitch":{"access_token":"oauth:stored-secret","expires_at":"oauth:bad-time"}}`)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--mock", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#example") {
		t.Fatalf("mock chat output missing channel: %q", stdout.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "oauth:stored-secret", "stored-secret", "oauth:bad-time", "bad-time")
}

func TestLiveChatMissingCredentialsAreActionableAndRedacted(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"TWI_TWITCH_USERNAME", "chat:read", "chat:edit", "--mock"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "oauth:secret-token") {
		t.Fatalf("stderr leaked token: %q", stderr.String())
	}
}

func TestLiveChatConfiguredStartsClient(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	oldNewLiveChatClient := newLiveChatClient
	oldRunLiveChat := runLiveChat
	defer func() {
		newLiveChatClient = oldNewLiveChatClient
		runLiveChat = oldRunLiveChat
	}()

	var gotChannels []string
	fake := app.NewFakeChatClient(1)
	newLiveChatClient = func(_ context.Context, cfg config.Config, _ debuglog.Logger) (app.ChatClient, error) {
		gotChannels = append([]string(nil), cfg.DefaultChannels...)
		return fake, nil
	}
	runLiveChat = func(stdout io.Writer, cfg config.Config, client app.ChatClient, opts app.ClientOptions) error {
		if client != fake {
			t.Fatalf("runLiveChat client = %#v, want fake", client)
		}
		if opts.AvatarResolver != nil {
			t.Fatalf("AvatarResolver = %#v, want nil for default initials mode", opts.AvatarResolver)
		}
		_, err := stdout.Write([]byte("live shell started\n"))
		return err
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "oauth:secret-token") {
		t.Fatalf("stderr leaked token: %q", stderr.String())
	}
	if got, want := strings.Join(gotChannels, ","), "example"; got != want {
		t.Fatalf("factory channels = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "live shell started") {
		t.Fatalf("stdout missing live shell output: %q", stdout.String())
	}
}

func TestLiveChatEnvCredentialsIgnoreUnsupportedCredentialFileFallback(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	oldNewCredentialStore := newCredentialStore
	oldNewLiveChatClient := newLiveChatClient
	oldRunLiveChat := runLiveChat
	t.Cleanup(func() {
		newCredentialStore = oldNewCredentialStore
		newLiveChatClient = oldNewLiveChatClient
		runLiveChat = oldRunLiveChat
	})

	newCredentialStore = func() (storage.CredentialStore, error) {
		return nil, fmt.Errorf("%w: credential-file fallback is disabled on non-Unix builds; use env/config; oauth:stored-secret", storage.ErrUnsupportedCredentialFilePlatform)
	}
	fake := app.NewFakeChatClient(1)
	newLiveChatClient = func(context.Context, config.Config, debuglog.Logger) (app.ChatClient, error) {
		return fake, nil
	}
	runLiveChat = func(stdout io.Writer, _ config.Config, client app.ChatClient, _ app.ClientOptions) error {
		if client != fake {
			t.Fatalf("runLiveChat client = %#v, want fake", client)
		}
		_, err := stdout.Write([]byte("live shell started\n"))
		return err
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "live shell started") {
		t.Fatalf("stdout missing live shell output: %q", stdout.String())
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "oauth:secret-token", "secret-token", "stored-secret")
}

func TestLiveChatConfiguredStartsClientWithMultipleChannels(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	oldNewLiveChatClient := newLiveChatClient
	oldRunLiveChat := runLiveChat
	defer func() {
		newLiveChatClient = oldNewLiveChatClient
		runLiveChat = oldRunLiveChat
	}()

	var gotFactoryChannels []string
	var gotRunChannels []string
	fake := app.NewFakeChatClient(1)
	newLiveChatClient = func(_ context.Context, cfg config.Config, _ debuglog.Logger) (app.ChatClient, error) {
		gotFactoryChannels = append([]string(nil), cfg.DefaultChannels...)
		return fake, nil
	}
	runLiveChat = func(_ io.Writer, cfg config.Config, client app.ChatClient, _ app.ClientOptions) error {
		if client != fake {
			t.Fatalf("runLiveChat client = %#v, want fake", client)
		}
		gotRunChannels = append([]string(nil), cfg.DefaultChannels...)
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "alpha", "--channel", "#Beta"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "currently supports one channel") {
		t.Fatalf("stderr rejected multi-channel live mode: %q", stderr.String())
	}
	if got, want := strings.Join(gotFactoryChannels, ","), "alpha,Beta"; got != want {
		t.Fatalf("factory channels = %q, want %q", got, want)
	}
	if got, want := strings.Join(gotRunChannels, ","), "alpha,Beta"; got != want {
		t.Fatalf("run channels = %q, want %q", got, want)
	}
}

func TestLiveChatConfiguredWiresImageStackWhenReady(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")
	t.Setenv("TWI_TWITCH_CLIENT_ID", "fixture-client")
	t.Setenv("TWI_IMAGE_MODE", "normal")
	t.Setenv("TWI_AVATAR_MODE", "image")
	t.Setenv("TWI_EMOJI_MODE", "image")
	t.Setenv("TWI_EMOTE_MODE", "image")
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("KITTY_WINDOW_ID", "42")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	oldNewLiveChatClient := newLiveChatClient
	oldRunLiveChat := runLiveChat
	defer func() {
		newLiveChatClient = oldNewLiveChatClient
		runLiveChat = oldRunLiveChat
	}()

	fake := app.NewFakeChatClient(1)
	newLiveChatClient = func(context.Context, config.Config, debuglog.Logger) (app.ChatClient, error) {
		return fake, nil
	}
	var got app.ClientOptions
	runLiveChat = func(_ io.Writer, _ config.Config, client app.ChatClient, opts app.ClientOptions) error {
		if client != fake {
			t.Fatalf("runLiveChat client = %#v, want fake", client)
		}
		got = opts
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, ok := got.AvatarResolver.(*assets.AvatarBatchResolver); !ok {
		t.Fatalf("AvatarResolver = %T, want *assets.AvatarBatchResolver", got.AvatarResolver)
	}
	if _, ok := got.AssetResolver.(*assets.Resolver); !ok {
		t.Fatalf("AssetResolver = %T, want *assets.Resolver", got.AssetResolver)
	}
	preparer, ok := got.ImagePreparer.(*render.PNGImagePreparer)
	if !ok {
		t.Fatalf("ImagePreparer = %T, want *render.PNGImagePreparer", got.ImagePreparer)
	}
	if preparer.Options.PreparedCache == nil || preparer.Options.PreparedDir != "" {
		t.Fatalf("ImagePreparer options = %#v, want cache-backed prepared outputs without standalone prepared dir", preparer.Options)
	}
	if _, ok := got.ImageRenderer.(*render.KittyRenderer); !ok {
		t.Fatalf("ImageRenderer = %T, want *render.KittyRenderer", got.ImageRenderer)
	}
	for _, kind := range []string{assets.KindAvatar, assets.KindBadge, assets.KindTwitchEmote, assets.KindEmoji} {
		if !got.AssetKinds[kind] {
			t.Fatalf("AssetKinds[%q] = false, want true; kinds=%#v", kind, got.AssetKinds)
		}
	}
	if strings.Contains(stderr.String(), "oauth:secret-token") {
		t.Fatalf("stderr leaked token: %q", stderr.String())
	}
}

func TestLiveClientOptionsGateImageStackByTerminalAndCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:secret-token"
	cfg.Twitch.ClientID = "fixture-client"
	cfg.Features.ImageMode = "auto"
	cfg.Features.AvatarMode = "image"
	cfg.Features.EmojiMode = "image"
	cfg.Features.EmoteMode = "image"

	unsupported := liveClientOptions(cfg, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, t.TempDir())
	if unsupported.AssetResolver != nil || unsupported.ImageRenderer != nil || unsupported.AvatarResolver != nil {
		t.Fatalf("unsupported auto options = %#v, want no live image stack", unsupported)
	}

	cfg.Features.ImageMode = "normal"
	cfg.Twitch.ClientID = ""
	partial := liveClientOptions(cfg, []string{"TERM=xterm-kitty", "COLORTERM=truecolor", "KITTY_WINDOW_ID=42"}, t.TempDir())
	if partial.AssetResolver == nil || partial.ImageRenderer == nil || partial.ImagePreparer == nil {
		t.Fatalf("partial emoji stack options = %#v, want resolver/preparer/renderer", partial)
	}
	if partial.AvatarResolver != nil {
		t.Fatalf("partial AvatarResolver = %T, want nil without Twitch API client ID", partial.AvatarResolver)
	}
	if got, want := assetKindNames(partial.AssetKinds), []string{assets.KindEmoji}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("partial AssetKinds = %#v, want %#v", got, want)
	}
}

func TestConfigShowRedactsSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	var stdout, stderr bytes.Buffer
	cfgPath := filepath.Join(t.TempDir(), "missing.toml?state=config-path-secret&code=config-code-secret")
	code := Run([]string{"config", "show", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, secret := range []string{"oauth:secret", "client-secret"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("config output leaked %q: %s", secret, stdout.String())
		}
	}
}

func TestConfigShowLoadsStoredCredentialsAndRedactsTokens(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeStoredCredentialFixture(t, storage.CredentialRecord{
		UserID:       "42",
		Login:        "viewer",
		DisplayName:  "Viewer",
		ClientID:     "client-id",
		AccessToken:  auth.NewSecret("stored-access-token"),
		RefreshToken: auth.NewSecret("stored-refresh-secret"),
		TokenType:    "bearer",
		Scopes:       auth.RequiredChatScopes(),
		UpdatedAt:    time.Now(),
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"config", "show", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{
		`twitch_username = "viewer"`,
		`twitch_oauth_token = "[redacted]"`,
		`twitch_refresh_token = "[redacted]"`,
		`twitch_client_id = "client-id"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config output missing %q:\n%s", want, stdout.String())
		}
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "stored-access-token", "stored-refresh-secret", "config-path-secret", "config-code-secret")
}

func TestConfigShowIgnoresUnsupportedCredentialFileFallback(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	oldNewCredentialStore := newCredentialStore
	t.Cleanup(func() {
		newCredentialStore = oldNewCredentialStore
	})

	for _, tc := range []struct {
		name string
		hook func() (storage.CredentialStore, error)
	}{
		{
			name: "constructor error",
			hook: func() (storage.CredentialStore, error) {
				return nil, fmt.Errorf("%w: credential-file fallback is disabled on non-Unix builds; oauth:stored-secret", storage.ErrUnsupportedCredentialFilePlatform)
			},
		},
		{
			name: "load error",
			hook: func() (storage.CredentialStore, error) {
				return storage.FailingCredentialStore{Err: fmt.Errorf("%w: credential-file fallback is disabled on non-Unix builds; oauth:stored-secret", storage.ErrUnsupportedCredentialFilePlatform)}, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newCredentialStore = tc.hook

			var stdout, stderr bytes.Buffer
			code := Run([]string{"config", "show", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

			if code != 0 {
				t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), `twitch_username = "viewer"`) || !strings.Contains(stdout.String(), `twitch_oauth_token = "[redacted]"`) {
				t.Fatalf("config output missing env credentials:\n%s", stdout.String())
			}
			assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "secret-token", "stored-secret")
		})
	}
}

func TestDoctorDoesNotPrintSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:access-token-private")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "refresh-secret")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	oldBuildDoctorReport := buildDoctorReport
	defer func() {
		buildDoctorReport = oldBuildDoctorReport
	}()
	buildDoctorReport = func(ctx context.Context, cfg config.Config, cfgErr error) app.DoctorReport {
		validator := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
			Result: twitch.TokenValidationResult{
				Status: twitch.TokenValidationMalformed,
				Detail: "Twitch rejected oauth:access-token-private with Bearer bearer-secret, client_secret=client-secret, refresh_token=refresh-secret, authorization_code=auth-code-secret",
			},
		})
		return app.DoctorWithOptions(ctx, cfg, app.DoctorOptions{
			Environ:         []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			CacheDir:        t.TempDir(),
			ConfigLoadError: cfgErr,
			TokenValidator:  validator,
			ReachabilityProbe: func(context.Context) error {
				return nil
			},
		})
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"[warn] config file:", "[ok] oauth token: present", "[warn] token validation:", "[redacted]"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q: %s", want, stdout.String())
		}
	}
	for _, secret := range []string{"oauth:access-token-private", "access-token-private", "bearer-secret", "client-secret", "refresh-secret", "auth-code-secret"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("doctor output leaked %q: %s", secret, stdout.String())
		}
	}
}

func TestDoctorDebugLogWritesRedactedCommandEvents(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:doctor-access-secret")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "doctor-refresh-secret")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "doctor-client-secret")
	logPath := filepath.Join(t.TempDir(), "debug.log")

	oldBuildDoctorReport := buildDoctorReport
	t.Cleanup(func() {
		buildDoctorReport = oldBuildDoctorReport
	})
	buildDoctorReport = func(context.Context, config.Config, error) app.DoctorReport {
		return app.DoctorReport{Checks: []app.DoctorCheck{{
			Name:   "fixture",
			Status: app.DoctorStatusOK,
			Detail: "ok",
		}}}
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"doctor",
		"--config", t.TempDir() + "/missing.toml",
		"--debug-log",
		"--debug-log-path", logPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile debug log returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{`"event":"cli.doctor.start"`, `"event":"cli.doctor.complete"`, `"check_count":1`} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
	assertOutputDoesNotContain(t, output, "oauth:doctor-access-secret", "doctor-access-secret", "doctor-refresh-secret", "doctor-client-secret")
}

func TestDoctorWarnsWhenCredentialFileFallbackUnsupported(t *testing.T) {
	oldNewCredentialStore := newCredentialStore
	oldBuildDoctorReport := buildDoctorReport
	t.Cleanup(func() {
		newCredentialStore = oldNewCredentialStore
		buildDoctorReport = oldBuildDoctorReport
	})

	newCredentialStore = func() (storage.CredentialStore, error) {
		return nil, fmt.Errorf("%w: credential-file fallback is disabled on non-Unix builds; use environment variables or a private flat config file", storage.ErrUnsupportedCredentialFilePlatform)
	}
	buildDoctorReport = func(context.Context, config.Config, error) app.DoctorReport {
		return app.DoctorReport{}
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"[warn] credential file:", "disabled on non-Unix builds", "environment variables", "private flat config file", "using env/config/defaults"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q: %s", want, stdout.String())
		}
	}
}

func TestDoctorLoadsStoredCredentialsWithoutPrintingTokens(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "state=credential-path-secret"))
	writeStoredCredentialFixture(t, storage.CredentialRecord{
		UserID:       "42",
		Login:        "viewer",
		DisplayName:  "Viewer",
		ClientID:     "client-id",
		AccessToken:  auth.NewSecret("stored-access-token"),
		RefreshToken: auth.NewSecret("stored-refresh-secret"),
		TokenType:    "bearer",
		Scopes:       auth.RequiredChatScopes(),
		UpdatedAt:    time.Now(),
	})

	fake := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Result: twitch.TokenValidationResult{
			Status:   twitch.TokenValidationValid,
			Identity: twitch.TokenIdentity{UserID: "42", Login: "viewer"},
			Scopes:   twitch.RequiredIRCScopes(),
		},
	})

	oldNewDoctorTokenValidator := newDoctorTokenValidator
	oldDoctorReachabilityProbe := doctorReachabilityProbe
	oldDoctorCacheDir := doctorCacheDir
	defer func() {
		newDoctorTokenValidator = oldNewDoctorTokenValidator
		doctorReachabilityProbe = oldDoctorReachabilityProbe
		doctorCacheDir = oldDoctorCacheDir
	}()
	newDoctorTokenValidator = func() twitch.TokenValidator {
		return fake
	}
	doctorReachabilityProbe = func(context.Context) error {
		return nil
	}
	doctorCacheDir = func() string {
		return t.TempDir()
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"[ok] credential file:", "[ok] twitch username: present", "[ok] oauth token: present", "[ok] token validation:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("validator requests = %d, want 1", len(requests))
	}
	if requests[0].Username != "viewer" || requests[0].OAuthToken != "oauth:stored-access-token" || requests[0].RefreshToken != "stored-refresh-secret" || requests[0].ClientID != "client-id" {
		t.Fatalf("validator request = %#v, want stored credentials", requests[0])
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(), "stored-access-token", "stored-refresh-secret", "credential-path-secret")
}

func TestDoctorEnvCredentialsTakePrecedenceOverStoredCredentials(t *testing.T) {
	clearTwitchCredentialEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TWI_TWITCH_USERNAME", "env_viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:env-access-token")
	t.Setenv("TWI_TWITCH_REFRESH_TOKEN", "env-refresh-secret")
	t.Setenv("TWI_TWITCH_CLIENT_ID", "env-client-id")
	writeStoredCredentialFixture(t, storage.CredentialRecord{
		UserID:       "42",
		Login:        "stored_viewer",
		DisplayName:  "StoredViewer",
		ClientID:     "stored-client-id",
		AccessToken:  auth.NewSecret("stored-access-token"),
		RefreshToken: auth.NewSecret("stored-refresh-secret"),
		TokenType:    "bearer",
		Scopes:       auth.RequiredChatScopes(),
		UpdatedAt:    time.Now(),
	})

	fake := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Result: twitch.TokenValidationResult{
			Status:   twitch.TokenValidationValid,
			Identity: twitch.TokenIdentity{UserID: "42", Login: "env_viewer"},
			Scopes:   twitch.RequiredIRCScopes(),
		},
	})

	oldNewDoctorTokenValidator := newDoctorTokenValidator
	oldDoctorReachabilityProbe := doctorReachabilityProbe
	oldDoctorCacheDir := doctorCacheDir
	defer func() {
		newDoctorTokenValidator = oldNewDoctorTokenValidator
		doctorReachabilityProbe = oldDoctorReachabilityProbe
		doctorCacheDir = oldDoctorCacheDir
	}()
	newDoctorTokenValidator = func() twitch.TokenValidator {
		return fake
	}
	doctorReachabilityProbe = func(context.Context) error {
		return nil
	}
	doctorCacheDir = func() string {
		return t.TempDir()
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("validator requests = %d, want 1", len(requests))
	}
	if requests[0].Username != "env_viewer" || requests[0].OAuthToken != "oauth:env-access-token" || requests[0].RefreshToken != "env-refresh-secret" || requests[0].ClientID != "env-client-id" {
		t.Fatalf("validator request = %#v, want env credentials to win", requests[0])
	}
	assertOutputDoesNotContain(t, stdout.String()+stderr.String(),
		"env-access-token", "env-refresh-secret", "stored-access-token", "stored-refresh-secret")
}

func TestDefaultDoctorReportWiresTokenValidator(t *testing.T) {
	cfg := config.Default()
	cfg.Path = t.TempDir() + "/missing.toml"
	cfg.Twitch.Username = "viewer"
	cfg.Twitch.OAuthToken = "oauth:access-token-private"

	fake := twitch.NewFakeTokenValidator(twitch.FakeTokenValidationOutcome{
		Result: twitch.TokenValidationResult{
			Status:   twitch.TokenValidationValid,
			Identity: twitch.TokenIdentity{UserID: "42", Login: "viewer"},
			Scopes:   twitch.RequiredIRCScopes(),
		},
	})

	oldNewDoctorTokenValidator := newDoctorTokenValidator
	oldDoctorReachabilityProbe := doctorReachabilityProbe
	oldDoctorCacheDir := doctorCacheDir
	defer func() {
		newDoctorTokenValidator = oldNewDoctorTokenValidator
		doctorReachabilityProbe = oldDoctorReachabilityProbe
		doctorCacheDir = oldDoctorCacheDir
	}()
	newDoctorTokenValidator = func() twitch.TokenValidator {
		return fake
	}
	doctorReachabilityProbe = func(context.Context) error {
		return nil
	}
	doctorCacheDir = func() string {
		return t.TempDir()
	}

	report := buildDoctorReport(context.Background(), cfg, nil)

	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("validator requests = %d, want 1", len(requests))
	}
	if requests[0].Username != "viewer" || requests[0].OAuthToken != "oauth:access-token-private" {
		t.Fatalf("validator request = %#v, want configured credentials", requests[0])
	}
	validation := doctorCheck(t, report, "token validation")
	if validation.Status != app.DoctorStatusOK {
		t.Fatalf("token validation status = %q, want ok; detail=%q", validation.Status, validation.Detail)
	}
	if strings.Contains(validation.Detail, "oauth:access-token-private") || strings.Contains(validation.Detail, "access-token-private") {
		t.Fatalf("token validation leaked token: %q", validation.Detail)
	}
}

func TestDoctorReportsConfigLoadErrorAndUsesEnvFallback(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret")

	dir := t.TempDir()
	path := dir + "/bad.toml"
	if err := os.WriteFile(path, []byte("not a key value line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldBuildDoctorReport := buildDoctorReport
	defer func() {
		buildDoctorReport = oldBuildDoctorReport
	}()
	buildDoctorReport = func(ctx context.Context, cfg config.Config, cfgErr error) app.DoctorReport {
		if cfgErr == nil {
			t.Fatal("doctor report builder received nil config error, want parse error")
		}
		if cfg.Twitch.Username != "viewer" || cfg.Twitch.OAuthToken != "oauth:secret" {
			t.Fatalf("fallback credentials = (%q, %q), want env values", cfg.Twitch.Username, cfg.Twitch.OAuthToken)
		}
		return app.DoctorWithOptions(ctx, cfg, app.DoctorOptions{
			Environ:         []string{"TERM=xterm-256color"},
			CacheDir:        t.TempDir(),
			ConfigLoadError: cfgErr,
			ReachabilityProbe: func(context.Context) error {
				return nil
			},
		})
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"[warn] config file:", "load failed", "[ok] oauth token: present"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q: %s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "oauth:secret") {
		t.Fatalf("doctor output leaked token: %s", stdout.String())
	}
}

func doctorCheck(t *testing.T, report app.DoctorReport, name string) app.DoctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor report missing check %q: %#v", name, report.Checks)
	return app.DoctorCheck{}
}

func assetKindNames(kinds map[string]bool) []string {
	order := []string{assets.KindAvatar, assets.KindBadge, assets.KindTwitchEmote, assets.KindEmoji}
	var names []string
	for _, kind := range order {
		if kinds[kind] {
			names = append(names, kind)
		}
	}
	return names
}

type fakeLoginCallbackWaiter struct {
	callback auth.LoginCallback
	err      error
	closed   bool
}

func (w *fakeLoginCallbackWaiter) Wait(ctx context.Context, expectedState auth.Secret) (auth.LoginCallback, error) {
	if err := ctx.Err(); err != nil {
		return auth.LoginCallback{}, err
	}
	if w.err != nil {
		return auth.LoginCallback{}, w.err
	}
	callback := w.callback
	if !callback.ExpectedState.Present() {
		callback.ExpectedState = expectedState
	}
	return callback, nil
}

func (w *fakeLoginCallbackWaiter) Close() error {
	w.closed = true
	return nil
}

func resetLoginTestHooks(t *testing.T) {
	t.Helper()

	oldNewLoginFlow := newLoginFlow
	oldNewLoginContext := newLoginContext
	oldNewLoginCallbackWaiter := newLoginCallbackWaiter
	oldOpenLoginBrowser := openLoginBrowser
	oldNewCredentialStore := newCredentialStore
	t.Cleanup(func() {
		newLoginFlow = oldNewLoginFlow
		newLoginContext = oldNewLoginContext
		newLoginCallbackWaiter = oldNewLoginCallbackWaiter
		openLoginBrowser = oldOpenLoginBrowser
		newCredentialStore = oldNewCredentialStore
	})
}

func freeLocalPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener addr = %T, want *net.TCPAddr", listener.Addr())
	}
	return addr.Port
}

func assertOutputDoesNotContain(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(output, value) {
			t.Fatalf("output leaked %q: %s", value, output)
		}
	}
}

func clearTwitchCredentialEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TWI_TWITCH_USERNAME",
		"TWI_TWITCH_OAUTH_TOKEN",
		"TWI_TWITCH_REFRESH_TOKEN",
		"TWI_TWITCH_CLIENT_ID",
		"TWI_TWITCH_CLIENT_SECRET",
		"TWITCH_USERNAME",
		"TWITCH_ACCESS_TOKEN",
		"TWITCH_REFRESH_TOKEN",
		"TWITCH_CLIENT_ID",
		"TWITCH_CLIENT_SECRET",
	} {
		t.Setenv(key, "")
	}
}

func writeStoredCredentialFixture(t *testing.T, record storage.CredentialRecord) {
	t.Helper()
	store, err := storage.NewDefaultCredentialFileStore()
	if err != nil {
		t.Fatalf("NewDefaultCredentialFileStore returned error: %v", err)
	}
	if err := store.SaveCredentials(context.Background(), record); err != nil {
		t.Fatalf("SaveCredentials fixture returned error: %v", err)
	}
}

func writeRawStoredCredentialFile(t *testing.T, content string) {
	t.Helper()
	path, err := storage.DefaultCredentialFilePath()
	if err != nil {
		t.Fatalf("DefaultCredentialFilePath returned error: %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, storage.CredentialDirectoryMode); err != nil {
		t.Fatalf("mkdir credential dir: %v", err)
	}
	if err := os.Chmod(dir, storage.CredentialDirectoryMode); err != nil {
		t.Fatalf("chmod credential dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), storage.CredentialFileMode); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	if err := os.Chmod(path, storage.CredentialFileMode); err != nil {
		t.Fatalf("chmod credential file: %v", err)
	}
}

type saveFailCredentialStore struct {
	err error
}

func (s saveFailCredentialStore) LoadCredentials(ctx context.Context) (storage.CredentialRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return storage.CredentialRecord{}, false, err
	}
	return storage.CredentialRecord{}, false, nil
}

func (s saveFailCredentialStore) SaveCredentials(ctx context.Context, _ storage.CredentialRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.err
}

func (s saveFailCredentialStore) DeleteCredentials(ctx context.Context) error {
	return ctx.Err()
}

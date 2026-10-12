package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
)

type deviceLoginStub struct{ canceled bool }

func (*deviceLoginStub) BeginDevice(context.Context, auth.LoginRequest) (auth.DeviceChallenge, error) {
	return auth.DeviceChallenge{DeviceCode: auth.NewSecret("private-device"), UserCode: auth.NewSecret("USERCODE"), VerificationURI: "https://www.twitch.tv/activate?device-code=USERCODE"}, nil
}

func (f *deviceLoginStub) WaitDevice(ctx context.Context, request auth.LoginRequest, _ auth.DeviceChallenge) (auth.LoginResult, error) {
	if f.canceled {
		return auth.LoginResult{}, ctx.Err()
	}
	return auth.LoginResult{Identity: auth.Identity{UserID: "123", Login: "owner"}, Scopes: request.Scopes, Tokens: auth.TokenSet{AccessToken: auth.NewSecret("private-access"), RefreshToken: auth.NewSecret("private-refresh"), TokenType: "bearer", ExpiresAt: time.Now().Add(time.Hour), Scopes: request.Scopes}}, nil
}

func TestDeviceLoginStoresSelectedAccountAndPrintsOnlyUserChallenge(t *testing.T) {
	r := newRunner()
	store := &storage.MemoryCredentialStore{}
	r.account = "validation"
	r.newNamedCredentialStore = func(name string) (storage.CredentialStore, error) {
		if name != "validation" {
			t.Fatal(name)
		}
		return store, nil
	}
	r.newCredentialStore = func() (storage.CredentialStore, error) { t.Fatal("named account fell back"); return nil, nil }
	r.newDeviceLoginFlow = func() auth.DeviceLoginFlow { return &deviceLoginStub{} }
	var stdout, stderr bytes.Buffer
	cfg := config.Config{}
	cfg.Twitch.ClientID = "client"
	cfg.Twitch.ClientSecret = "private-client"
	if code := r.deviceLoginAndSave(context.Background(), cfg, loginOptions{timeout: time.Second, capability: "chat-read"}, &stdout, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if len(store.SavedRecords()) != 1 || !strings.Contains(stdout.String(), "USERCODE") {
		t.Fatal("grant not stored or user code missing")
	}
	for _, secret := range []string{"private-device", "private-access", "private-refresh", "private-client"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
}

func TestDeviceLoginCanceledParentNeverSaves(t *testing.T) {
	r := newRunner()
	store := &storage.MemoryCredentialStore{}
	r.newCredentialStore = func() (storage.CredentialStore, error) { return store, nil }
	r.newDeviceLoginFlow = func() auth.DeviceLoginFlow { return &deviceLoginStub{canceled: true} }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.Config{}
	cfg.Twitch.ClientID = "client"
	var output bytes.Buffer
	if code := r.deviceLoginAndSave(ctx, cfg, loginOptions{timeout: time.Second, capability: "chat-read"}, &output, &output); code == 0 || len(store.SavedRecords()) != 0 {
		t.Fatal("canceled login saved tokens")
	}
}

func TestDeviceFlagConflictAndDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, code, ok := parseLoginFlags([]string{"--device", "--redirect-uri", defaultLoginRedirectURI}, &stdout, &stderr); ok || code != 2 {
		t.Fatal("callback and device flags accepted")
	}
	r := newRunner()
	r.newDeviceLoginFlow = func() auth.DeviceLoginFlow { t.Fatal("dry-run attempted network"); return nil }
	r.newCredentialStore = func() (storage.CredentialStore, error) { return nil, errors.New("must not access store") }
	stdout.Reset()
	stderr.Reset()
	if code := r.runLoginContext(context.Background(), []string{"--device", "--dry-run", "--capability", "chat-read", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Device login") {
		t.Fatal(stdout.String())
	}
}

func TestLoginSuccessRedactsReflectedSecretsInScopeMetadata(t *testing.T) {
	var output bytes.Buffer
	result := auth.LoginResult{Identity: auth.Identity{Login: "owner"}, Scopes: []auth.Scope{"chat:read", "private-access"}, Tokens: auth.TokenSet{AccessToken: auth.NewSecret("private-access")}}
	printLoginSuccess(&output, result, result.Redactor(), "validation")
	if strings.Contains(output.String(), "private-access") || !strings.Contains(output.String(), "chat:read") {
		t.Fatal("scope metadata leaked a known secret")
	}
}

package runtimeauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
)

type validatorFunc func(context.Context, twitch.TokenCredentials) (twitch.TokenValidationResult, error)

func (f validatorFunc) ValidateToken(ctx context.Context, credentials twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
	return f(ctx, credentials)
}
func TestReadCapabilityDoesNotRequireSend(t *testing.T) {
	validator := validatorFunc(func(context.Context, twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
		return twitch.TokenValidationResult{Status: twitch.TokenValidationMissingScope, Scopes: []auth.Scope{auth.ScopeChatRead}, Identity: twitch.TokenIdentity{UserID: "42", Login: "reader"}}, nil
	})
	manager := New(Config{Required: []auth.Scope{auth.ScopeChatRead}, Validator: validator})
	if err := manager.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager = New(Config{Required: []auth.Scope{auth.ScopeUserWriteChat}, Validator: validator})
	if err := manager.Validate(context.Background()); err == nil || !strings.Contains(err.Error(), "auth_scope_missing") {
		t.Fatalf("missing scope: %v", err)
	}
}
func TestExpiredTokenRefreshAndPersistence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	refreshCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Error("refresh fields")
		}
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh"}`))
	}))
	defer server.Close()
	persisted := false
	credentials := twitch.TokenCredentials{Username: "reader", OAuthToken: "oauth:old-access", RefreshToken: "old-refresh", ClientID: "client", ClientSecret: "secret"}
	validator := validatorFunc(func(_ context.Context, c twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
		if c.OAuthToken == "oauth:old-access" {
			return twitch.TokenValidationResult{Status: twitch.TokenValidationExpired}, nil
		}
		return twitch.TokenValidationResult{Status: twitch.TokenValidationMissingScope, Identity: twitch.TokenIdentity{Login: "reader", UserID: "42"}, Scopes: []auth.Scope{auth.ScopeChatRead}, ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	manager := New(Config{Credentials: credentials, Required: []auth.Scope{auth.ScopeChatRead}, Validator: validator, TokenURL: server.URL, Reload: func(context.Context) (twitch.TokenCredentials, error) { return credentials, nil }, Persist: func(_ context.Context, c twitch.TokenCredentials) error {
		persisted = true
		if c.RefreshToken != "new-refresh" {
			t.Error("rotated refresh missing")
		}
		return nil
	}})
	if err := manager.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Token() != "oauth:new-access" || !persisted || refreshCalls != 1 {
		t.Fatalf("token %q persisted %v calls %d", manager.Token(), persisted, refreshCalls)
	}
}
func TestRefreshUsesOtherProcessToken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	credentials := twitch.TokenCredentials{Username: "reader", OAuthToken: "old", RefreshToken: "refresh", ClientID: "client", ClientSecret: "secret"}
	manager := New(Config{Credentials: credentials, Required: []auth.Scope{auth.ScopeChatRead}, Validator: validatorFunc(func(_ context.Context, c twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
		if c.OAuthToken != "new" {
			t.Error("did not use reloaded token")
		}
		return twitch.TokenValidationResult{Status: twitch.TokenValidationValid, Scopes: []auth.Scope{auth.ScopeChatRead}, Identity: twitch.TokenIdentity{Login: "reader", UserID: "42"}}, nil
	}), Reload: func(context.Context) (twitch.TokenCredentials, error) {
		latest := credentials
		latest.OAuthToken = "new"
		return latest, nil
	}})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Token() != "new" {
		t.Fatal("token not adopted")
	}
}

func TestRefreshHTTPFailuresKeepErrorClass(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, status := range []int{400, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			manager := New(Config{Credentials: twitch.TokenCredentials{Username: "reader", OAuthToken: "old", RefreshToken: "refresh", ClientID: "client", ClientSecret: "secret"}, TokenURL: server.URL})
			err := manager.Refresh(context.Background())
			if err == nil {
				t.Fatal("missing error")
			}
			wantAuth := status == 400 || status == 401 || status == 403
			if errors.Is(err, twitch.ErrAuthFailed) != wantAuth {
				t.Fatalf("status %d error %v", status, err)
			}
		})
	}
}

package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/twitch"
)

type readOnlyValidator func(context.Context, twitch.TokenCredentials) (twitch.TokenValidationResult, error)

func (f readOnlyValidator) ValidateToken(ctx context.Context, c twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
	return f(ctx, c)
}
func TestHeadlessIRCRefreshKeepsReadOnlyCapabilityAndSinglePersistence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var refreshCalls, persistCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "refresh" {
			t.Error("wrong refresh token")
		}
		fmt.Fprint(w, `{"access_token":"replacement","refresh_token":"rotated","expires_in":3600,"scope":["chat:read"]}`)
	}))
	defer server.Close()
	creds := twitch.TokenCredentials{Username: "reader", OAuthToken: "old", RefreshToken: "refresh", ClientID: "client", ClientSecret: "secret"}
	manager := runtimeauth.New(runtimeauth.Config{Credentials: creds, Required: []auth.Scope{auth.ScopeChatRead}, TokenURL: server.URL, Validator: readOnlyValidator(func(context.Context, twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
		return twitch.TokenValidationResult{Status: twitch.TokenValidationMissingScope, Identity: twitch.TokenIdentity{UserID: "1", Login: "reader"}, Scopes: []auth.Scope{auth.ScopeChatRead}}, nil
	}), Persist: func(context.Context, twitch.TokenCredentials) error { persistCalls.Add(1); return nil }})
	if err := manager.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	config := headlessIRCConfig(manager, []string{"alpha"})
	refreshed, err := config.RefreshOAuth(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken.Reveal() != "oauth:replacement" || refreshed.RefreshToken.Reveal() != "rotated" || manager.Token() != "oauth:replacement" {
		t.Fatal("refreshed credentials not shared")
	}
	if config.OnOAuthRefresh != nil || refreshCalls.Load() != 1 || persistCalls.Load() != 1 {
		t.Fatalf("duplicate persistence refresh=%d persist=%d", refreshCalls.Load(), persistCalls.Load())
	}
	if next := headlessIRCConfig(manager, []string{"alpha"}); next.OAuthToken != "oauth:replacement" || next.RefreshToken != "rotated" {
		t.Fatal("reconnect factory retained stale credentials")
	}
}

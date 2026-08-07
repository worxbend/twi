package cli

import (
	"context"
	"testing"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/twitch"
)

func TestCredentialHolderReturnsInitialCredentials(t *testing.T) {
	holder := newCredentialHolder(config.TwitchConfig{
		Username:     "viewer",
		OAuthToken:   "oauth:first",
		RefreshToken: "refresh-first",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	})
	got := holder.current()
	if got.OAuthToken != "oauth:first" || got.RefreshToken != "refresh-first" {
		t.Fatalf("current() = %#v, want the credentials it was built with", got)
	}
}

func TestCredentialHolderAppliesRefresh(t *testing.T) {
	holder := newCredentialHolder(config.TwitchConfig{
		OAuthToken:   "oauth:old",
		RefreshToken: "refresh-old",
		ClientID:     "client-id",
	})
	holder.applyRefresh(twitch.OAuthRefresh{
		AccessToken:  auth.NewSecret("oauth:new"),
		RefreshToken: auth.NewSecret("refresh-new"),
	})

	got := holder.current()
	if got.OAuthToken != "oauth:new" {
		t.Errorf("OAuthToken = %q, want the refreshed token", got.OAuthToken)
	}
	if got.RefreshToken != "refresh-new" {
		t.Errorf("RefreshToken = %q, want the rotated refresh token", got.RefreshToken)
	}
	if got.ClientID != "client-id" {
		t.Errorf("ClientID = %q, want it preserved across a refresh", got.ClientID)
	}
}

// TestCredentialHolderKeepsRefreshTokenWhenNotRotated covers Twitch's actual
// behavior: it does not always issue a new refresh token, and the response
// then carries an empty one. Overwriting with that would discard a still-valid
// credential and make the next refresh impossible.
func TestCredentialHolderKeepsRefreshTokenWhenNotRotated(t *testing.T) {
	holder := newCredentialHolder(config.TwitchConfig{
		OAuthToken:   "oauth:old",
		RefreshToken: "refresh-keep",
	})
	holder.applyRefresh(twitch.OAuthRefresh{
		AccessToken: auth.NewSecret("oauth:new"),
	})

	if got := holder.current().RefreshToken; got != "refresh-keep" {
		t.Fatalf("RefreshToken = %q, want the existing token preserved", got)
	}
}

// TestTransportFactoryUsesRefreshedCredentials is the regression this exists
// for. The factory used to close over a config.Config by value, so ctrl+r --
// the recovery key the UI names when chat drops -- rebuilt the client with the
// pre-refresh access token and the already-rotated refresh token, which Twitch
// had invalidated. The documented way out was the thing that made the session
// unrecoverable.
func TestTransportFactoryUsesRefreshedCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch = config.TwitchConfig{
		Username:     "viewer",
		OAuthToken:   "oauth:stale",
		RefreshToken: "refresh-stale",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	holder := newCredentialHolder(cfg.Twitch)

	// A refresh lands mid-session, rotating both tokens.
	holder.applyRefresh(twitch.OAuthRefresh{
		AccessToken:  auth.NewSecret("oauth:fresh"),
		RefreshToken: auth.NewSecret("refresh-fresh"),
	})

	// What a subsequent ctrl+r would build the replacement transport from.
	ircCfg := liveIRCConfig(cfg, holder, debuglog.Logger{}, credentialLoadStatus{})
	if ircCfg.OAuthToken != "oauth:fresh" {
		t.Errorf("reconnect would use token %q, want the refreshed %q", ircCfg.OAuthToken, "oauth:fresh")
	}
	if ircCfg.RefreshToken != "refresh-fresh" {
		t.Errorf("reconnect would use refresh token %q, want the rotated %q", ircCfg.RefreshToken, "refresh-fresh")
	}
	if ircCfg.ClientSecret != "client-secret" {
		t.Errorf("reconnect lost the client secret: %q", ircCfg.ClientSecret)
	}
}

// TestTransportRefreshCallbackUpdatesHolder closes the loop: the callback the
// transport invokes on a successful refresh is what makes the holder current.
func TestTransportRefreshCallbackUpdatesHolder(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch = config.TwitchConfig{OAuthToken: "oauth:stale", RefreshToken: "refresh-stale"}
	holder := newCredentialHolder(cfg.Twitch)

	ircCfg := liveIRCConfig(cfg, holder, debuglog.Logger{}, credentialLoadStatus{})
	// Persisting fails here (no credential store), which must not stop the
	// in-memory update: the next reconnect reads the holder, not the disk.
	_ = ircCfg.OnOAuthRefresh(context.Background(), twitch.OAuthRefresh{
		AccessToken:  auth.NewSecret("oauth:fresh"),
		RefreshToken: auth.NewSecret("refresh-fresh"),
	})

	if got := holder.current().OAuthToken; got != "oauth:fresh" {
		t.Fatalf("holder token = %q, want %q after the refresh callback", got, "oauth:fresh")
	}
	if got := liveIRCConfig(cfg, holder, debuglog.Logger{}, credentialLoadStatus{}).OAuthToken; got != "oauth:fresh" {
		t.Fatalf("a later reconnect would use %q, want the refreshed token", got)
	}
}

func TestCredentialHolderIsSafeForConcurrentUse(t *testing.T) {
	holder := newCredentialHolder(config.TwitchConfig{OAuthToken: "oauth:start"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			holder.applyRefresh(twitch.OAuthRefresh{AccessToken: auth.NewSecret("oauth:next")})
		}
	}()
	for range 200 {
		_ = holder.current()
	}
	<-done
}

package runtimeapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
)

type appTokens struct{ calls int }

func (t *appTokens) Acquire(context.Context) (auth.Secret, time.Time, error) {
	t.calls++
	return auth.NewSecret("fake-app-token"), time.Now().Add(time.Hour), nil
}
func (t *appTokens) Validate(context.Context, auth.Secret) error { return nil }

type grants struct {
	senderScopes []auth.Scope
	channelBot   bool
	calls        int
}

func (g *grants) GetGrants(_ context.Context, ids []string) ([]helix.AuthorizationGrant, error) {
	g.calls++
	result := []helix.AuthorizationGrant{}
	for _, id := range ids {
		scopes := []auth.Scope{}
		if id == "sender" {
			scopes = g.senderScopes
		}
		if id == "broadcaster" && g.channelBot {
			scopes = []auth.Scope{"channel:bot"}
		}
		result = append(result, helix.AuthorizationGrant{UserID: id, Authorized: true, Scopes: scopes})
	}
	return result, nil
}

type moderatorProof struct {
	verified bool
	calls    int
}

func (m *moderatorProof) GetModeratedChannels(context.Context, string, []string) ([]twitch.ModeratedChannel, error) {
	m.calls++
	if m.verified {
		return []twitch.ModeratedChannel{{BroadcasterID: "broadcaster"}}, nil
	}
	return nil, nil
}
func TestAppAuthorizationRequiresFreshDelegatedSenderAndDestinationGrants(t *testing.T) {
	t.Parallel()
	tokens := &appTokens{}
	authority := &grants{senderScopes: []auth.Scope{"user:write:chat", "user:bot"}, channelBot: true}
	manager := New(Config{Tokens: tokens, Grants: authority, Sender: twitch.TokenIdentity{UserID: "sender", Login: "bot"}})
	if err := manager.ValidateIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeDestination(context.Background(), "broadcaster"); err != nil {
		t.Fatal(err)
	}
	authority.channelBot = false
	if err := manager.AuthorizeDestination(context.Background(), "broadcaster"); !errors.Is(err, twitch.ErrAuthFailed) {
		t.Fatalf("revoked destination grant ignored: %v", err)
	}
	authority.channelBot = true
	authority.senderScopes = []auth.Scope{"user:write:chat"}
	if err := manager.AuthorizeDestination(context.Background(), "broadcaster"); !errors.Is(err, twitch.ErrAuthFailed) {
		t.Fatalf("missing bot delegation ignored: %v", err)
	}
	if authority.calls != 3 || tokens.calls != 1 {
		t.Fatalf("fresh grant checks=%d token acquisitions=%d", authority.calls, tokens.calls)
	}
}
func TestAppModeratorAlternativeRequiresActualRoleProof(t *testing.T) {
	t.Parallel()
	authority := &grants{senderScopes: []auth.Scope{"user:write:chat", "user:bot", "user:read:moderated_channels"}}
	roles := &moderatorProof{verified: true}
	manager := New(Config{Tokens: &appTokens{}, Grants: authority, Moderators: roles, Sender: twitch.TokenIdentity{UserID: "sender"}})
	if err := manager.AuthorizeDestination(context.Background(), "broadcaster"); err != nil {
		t.Fatal(err)
	}
	if verified, _ := manager.ModeratorProof(context.Background(), "broadcaster"); !verified {
		t.Fatal("current moderator proof lost")
	}
	roles.verified = false
	if err := manager.AuthorizeDestination(context.Background(), "broadcaster"); !errors.Is(err, twitch.ErrAuthFailed) {
		t.Fatalf("role revocation ignored: %v", err)
	}
	if verified, _ := manager.ModeratorProof(context.Background(), "broadcaster"); verified {
		t.Fatal("stale moderator privilege retained")
	}
}

package cli

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/eventsub"
)

type moderatorLookup struct {
	calls  int
	user   string
	ids    []string
	result []twitch.ModeratedChannel
	err    error
}

func (m *moderatorLookup) GetModeratedChannels(_ context.Context, user string, ids []string) ([]twitch.ModeratedChannel, error) {
	m.calls++
	m.user = user
	m.ids = append([]string(nil), ids...)
	return m.result, m.err
}
func TestModeratorSubscriptionRequiresScopeAndSelectedRoleProof(t *testing.T) {
	t.Parallel()
	scopes, _ := auth.CapabilityScopes("moderation-read")
	lookup := &moderatorLookup{result: []twitch.ModeratedChannel{{BroadcasterID: "other"}, {BroadcasterID: "unselected"}, {BroadcasterID: "other"}}}
	ids, err := resolveModeratorChannels(context.Background(), lookup, twitch.TokenIdentity{UserID: "actor"}, scopes, []eventsub.Channel{{Login: "own", ID: "actor"}, {Login: "alpha", ID: "other"}, {Login: "beta", ID: "unproven"}})
	if err != nil {
		t.Fatal(err)
	}
	if lookup.calls != 1 || lookup.user != "actor" || !slices.Equal(lookup.ids, []string{"other", "unproven"}) || !slices.Equal(ids, []string{"other"}) {
		t.Fatalf("proof selection ids=%v user=%s selected=%v calls=%d", ids, lookup.user, lookup.ids, lookup.calls)
	}
}
func TestBasicReaderNeverInfersModeratorRoleFromScope(t *testing.T) {
	t.Parallel()
	for _, scopes := range [][]auth.Scope{{auth.ScopeUserReadChat}, auth.ModerationScopes(), {auth.ScopeUserReadChat, auth.Scope("user:read:moderated_channels")}} {
		lookup := &moderatorLookup{result: []twitch.ModeratedChannel{{BroadcasterID: "other"}}}
		ids, err := resolveModeratorChannels(context.Background(), lookup, twitch.TokenIdentity{UserID: "actor"}, scopes, []eventsub.Channel{{ID: "other"}})
		if err != nil || len(ids) != 0 || lookup.calls != 0 {
			t.Fatalf("unsupported inferred role ids=%v calls=%d err=%v", ids, lookup.calls, err)
		}
	}
}
func TestEnhancedRoleDiscoveryFailureIsNotCompleteCoverage(t *testing.T) {
	t.Parallel()
	scopes, _ := auth.CapabilityScopes("moderation-read")
	expected := errors.New("authority unavailable")
	lookup := &moderatorLookup{err: expected}
	if _, err := resolveModeratorChannels(context.Background(), lookup, twitch.TokenIdentity{UserID: "actor"}, scopes, []eventsub.Channel{{ID: "other"}}); !errors.Is(err, expected) {
		t.Fatalf("discovery failure hidden: %v", err)
	}
}

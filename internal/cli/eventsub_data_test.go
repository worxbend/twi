package cli

import (
	"github.com/worxbend/twi/internal/twitch"
	"testing"
)

func TestEventSubPartialIdentityResolution(t *testing.T) {
	selected := []string{"alpha", "missing", "beta"}
	users := []twitch.UserIdentity{{Login: "BETA", UserID: "2"}, {Login: "alpha", UserID: "1"}}
	if _, _, err := resolveEventSubChannels(selected, users, false); err == nil {
		t.Fatal("strict resolution accepted missing channel")
	}
	resolved, excluded, err := resolveEventSubChannels(selected, users, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].Login != "alpha" || resolved[0].ID != "1" || resolved[1].Login != "beta" || resolved[1].ID != "2" || len(excluded) != 1 || excluded[0] != "missing" {
		t.Fatalf("resolution=%+v exclusions=%v", resolved, excluded)
	}
	if _, _, err := resolveEventSubChannels([]string{"missing"}, users, true); err == nil {
		t.Fatal("partial selection without healthy channels succeeded")
	}
}

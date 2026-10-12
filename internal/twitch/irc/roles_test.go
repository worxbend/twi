package irc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	gempir "github.com/gempir/go-twitch-irc/v4"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
)

func TestOwnUserStateRoleProofAndRevocation(t *testing.T) {
	c := &Client{channels: []string{"alpha"}}
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("unknown role elevated")
	}
	for badge, role := range map[string]sendquota.Role{"broadcaster": sendquota.RoleBroadcaster, "moderator": sendquota.RoleModerator, "vip": sendquota.RoleVIP} {
		c.observeOwnRole(gempir.UserStateMessage{Channel: "alpha", User: gempir.User{Badges: map[string]int{badge: 1}}})
		if got := c.roleForChannel("#ALPHA"); got != role {
			t.Fatalf("badge=%s role=%s", badge, got)
		}
		c.observeOwnRole(gempir.UserStateMessage{Channel: "alpha", User: gempir.User{}})
		if c.roleForChannel("alpha") != sendquota.RoleNormal {
			t.Fatal("empty own state did not revoke")
		}
	}
	c.observeOwnRole(gempir.UserStateMessage{Channel: "unjoined", User: gempir.User{Badges: map[string]int{"moderator": 1}}})
	if c.roleForChannel("unjoined") != sendquota.RoleNormal {
		t.Fatal("unjoined role retained")
	}
	c.observeOwnRole(gempir.UserStateMessage{Channel: "alpha", User: gempir.User{Name: "broadcaster", IsMod: true, IsVip: true}})
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("names or flags elevated role without own badges")
	}
	c.observeOwnRole(gempir.UserStateMessage{Channel: "alpha", User: gempir.User{Badges: map[string]int{"moderator": 1}}})
	c.resetRoles()
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("reconnect retained role")
	}
}
func TestLocalMixedRoleLimits(t *testing.T) {
	now := time.Now()
	limiter := newSendLimiter(func() time.Time { return now })
	for i := range 20 {
		if err := limiter.allowForRole("alpha", fmt.Sprintf("normal%d", i), sendquota.RoleNormal); err != nil {
			t.Fatal(err)
		}
	}
	if err := limiter.allow("other", "normal overflow"); !errors.Is(err, twitch.ErrRateLimited) {
		t.Fatalf("normal limit %v", err)
	}
	for i := range 80 {
		if err := limiter.allowForRole("alpha", fmt.Sprintf("privilege%d", i), sendquota.RoleVIP); err != nil {
			t.Fatal(err)
		}
	}
	if err := limiter.allowForRole("alpha", "privilege overflow", sendquota.RoleModerator); !errors.Is(err, twitch.ErrRateLimited) {
		t.Fatalf("privileged limit %v", err)
	}
	now = now.Add(30 * time.Second)
	if err := limiter.allow("other", "window reset"); err != nil {
		t.Fatal(err)
	}
}

func TestRoleProofDoesNotSurviveDepartOrStaleSession(t *testing.T) {
	current := &fakeIRCSession{}
	c := &Client{channels: []string{"alpha"}, client: current}
	proof := gempir.UserStateMessage{Channel: "alpha", User: gempir.User{Badges: map[string]int{"moderator": 1}}}
	c.observeOwnRoleFrom(&fakeIRCSession{}, proof)
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("old session granted role")
	}
	c.observeOwnRoleFrom(current, proof)
	if c.roleForChannel("alpha") != sendquota.RoleModerator {
		t.Fatal("current own state ignored")
	}
	if err := c.Depart(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("depart retained privilege")
	}
	c.observeOwnRoleFrom(current, proof)
	if c.roleForChannel("alpha") != sendquota.RoleNormal {
		t.Fatal("late departed callback restored privilege")
	}
}

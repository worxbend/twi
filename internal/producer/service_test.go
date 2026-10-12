package producer

import (
	"context"
	"errors"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
	"testing"
)

type authorization struct {
	refreshes               int
	validateErr, refreshErr error
}

func (a *authorization) ValidateIfDue(context.Context) error { return a.validateErr }
func (a *authorization) Refresh(context.Context) error       { a.refreshes++; return a.refreshErr }
func (a *authorization) Identity() twitch.TokenIdentity {
	return twitch.TokenIdentity{Login: "account", UserID: "sender"}
}

func TestAcknowledgementAndAmbiguousSendContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                   string
		status                 int
		sent                   bool
		wantStatus             string
		wantFailure, wantCalls int
	}{{"acknowledged", 200, true, "sent", 0, 1}, {"drop", 200, false, "rejected", 5, 1}, {"ambiguous server error", 503, false, "unknown", 4, 1}, {"definitive authorization failure", 401, false, "rejected", 3, 2}, {"rate rejection", 429, false, "rejected", 5, 1}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			auth := &authorization{}
			calls, permits := 0, 0
			service, err := New(Config{Auth: auth, Resolve: func(context.Context, string) (string, error) { return "broadcaster", nil }, Permit: func(context.Context, string, string) error { permits++; return nil }, Send: func(_ context.Context, request Request) (Acknowledgement, int, error) {
				calls++
				if request.BroadcasterID != "broadcaster" || request.SenderID != "sender" || request.Reply != "parent" {
					t.Fatalf("routing=%+v", request)
				}
				var err error
				if test.status != 200 {
					err = errors.New("safe upstream error")
				}
				return Acknowledgement{MessageID: "message", Sent: test.sent, DropReason: &automation.DropReason{Code: "automod"}}, test.status, err
			}})
			if err != nil {
				t.Fatal(err)
			}
			receipt, failure := service.Deliver(context.Background(), "#Alpha", "hello", "parent")
			if receipt.Status != test.wantStatus || failure != test.wantFailure || calls != test.wantCalls || permits != 1 {
				t.Fatalf("receipt=%+v failure=%d calls=%d permits=%d", receipt, failure, calls, permits)
			}
		})
	}
}
func TestDeniedQuotaCannotSend(t *testing.T) {
	t.Parallel()
	service, err := New(Config{Auth: &authorization{}, Resolve: func(context.Context, string) (string, error) { return "channel", nil }, Permit: func(context.Context, string, string) error { return sendquota.ErrLimited }, Send: func(context.Context, Request) (Acknowledgement, int, error) {
		t.Fatal("send bypassed quota")
		return Acknowledgement{}, 0, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	receipt, code := service.Deliver(context.Background(), "alpha", "hello", "")
	if code != 5 || receipt.DropReason.Code != "rate_limited" {
		t.Fatalf("receipt=%+v code=%d", receipt, code)
	}
}
func TestAuthorizationFailureCannotResolveOrSend(t *testing.T) {
	t.Parallel()
	service, err := New(Config{Auth: &authorization{validateErr: twitch.ErrAuthFailed}, Resolve: func(context.Context, string) (string, error) {
		t.Fatal("identity lookup before authorization")
		return "", nil
	}, Permit: func(context.Context, string, string) error { t.Fatal("quota before authorization"); return nil }, Send: func(context.Context, Request) (Acknowledgement, int, error) {
		t.Fatal("send before authorization")
		return Acknowledgement{}, 0, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, code := service.Deliver(context.Background(), "alpha", "hello", "")
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
}

func TestQuotaRoleRequiresResolvedBroadcasterIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, id string
		role     sendquota.Role
	}{{"own broadcaster", "sender", sendquota.RoleBroadcaster}, {"other broadcaster", "other", sendquota.RoleNormal}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			service, err := New(Config{Auth: &authorization{}, Resolve: func(context.Context, string) (string, error) { return test.id, nil }, PermitRole: func(_ context.Context, account, channel string, role sendquota.Role) error {
				calls++
				if account != "account" || channel != "selected" || role != test.role {
					t.Fatalf("reservation account=%s channel=%s role=%s", account, channel, role)
				}
				return nil
			}, Send: func(context.Context, Request) (Acknowledgement, int, error) {
				return Acknowledgement{Sent: true}, 200, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, code := service.Deliver(context.Background(), "selected", "hello", "")
			if code != 0 || calls != 1 {
				t.Fatalf("code=%d permits=%d", code, calls)
			}
		})
	}
}

func TestModeratorQuotaRequiresFreshPositiveProof(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		verified bool
		err      error
		want     sendquota.Role
	}{{"verified", true, nil, sendquota.RoleModerator}, {"scope only", false, nil, sendquota.RoleNormal}, {"failed lookup", true, errors.New("unavailable"), sendquota.RoleNormal}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proofCalls := 0
			service, err := New(Config{Auth: &authorization{}, Resolve: func(context.Context, string) (string, error) { return "other-broadcaster", nil }, ModeratorProof: func(_ context.Context, id string) (bool, error) {
				proofCalls++
				if id != "other-broadcaster" {
					t.Fatal(id)
				}
				return test.verified, test.err
			}, PermitRole: func(_ context.Context, account, channel string, role sendquota.Role) error {
				if role != test.want {
					t.Fatalf("role=%s want=%s", role, test.want)
				}
				return nil
			}, Send: func(context.Context, Request) (Acknowledgement, int, error) {
				return Acknowledgement{Sent: true}, 200, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, code := service.Deliver(context.Background(), "alpha", "hello", ""); code != 0 {
					t.Fatal(code)
				}
			}
			if proofCalls != 2 {
				t.Fatalf("cached stale role proof: calls=%d", proofCalls)
			}
		})
	}
}

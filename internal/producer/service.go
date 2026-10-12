// Package producer owns acknowledged sending, authorization, routing, and quota
// decisions. The CLI owns input decoding and output streams; HTTP stays in adapters.
package producer

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
)

type Authorization interface {
	ValidateIfDue(context.Context) error
	Refresh(context.Context) error
	Identity() twitch.TokenIdentity
}
type Request struct {
	BroadcasterID, SenderID, Text, Reply string
	Channel                              string
	Action                               bool
}
type Acknowledgement struct {
	MessageID   string
	Sent        bool
	DropReason  *automation.DropReason
	Unconfirmed bool
}
type Config struct {
	AllowAction          bool
	AuthorizeDestination func(context.Context, string) error
	Close                func() error
	// ModeratorProof must query current server role state; errors retain normal pacing.
	ModeratorProof                func(context.Context, string) (bool, error)
	Auth                          Authorization
	Resolve                       func(context.Context, string) (string, error)
	Send                          func(context.Context, Request) (Acknowledgement, int, error)
	Permit                        func(context.Context, string, string) error
	PermitRole                    func(context.Context, string, string, sendquota.Role) error
	LookupTimeout, RefreshTimeout time.Duration
}

// Service is a serialized producer session. The input loop calls Deliver in order.
type Service struct {
	config   Config
	channels map[string]string
}

func New(config Config) (*Service, error) {
	if config.Auth == nil || config.Resolve == nil || config.Send == nil || config.Permit == nil && config.PermitRole == nil {
		return nil, errors.New("producer requires authorization, resolver, sender, and quota ports")
	}
	if config.LookupTimeout <= 0 {
		config.LookupTimeout = 30 * time.Second
	}
	if config.RefreshTimeout <= 0 {
		config.RefreshTimeout = 15 * time.Second
	}
	return &Service{config: config, channels: make(map[string]string)}, nil
}

// Deliver returns one acknowledgement and a CLI-compatible outcome category.
// Only a definitive HTTP 401 permits one refresh and retry. Ambiguous writes
// never retry here; an adapter may wait and retry definitive HTTP 429 rejections.
func (s *Service) Deliver(ctx context.Context, channel, text, reply string) (automation.SendResult, int) {
	return s.DeliverAction(ctx, channel, text, reply, false)
}
func (s *Service) DeliverAction(ctx context.Context, channel, text, reply string, action bool) (automation.SendResult, int) {
	receipt := automation.SendResult{SchemaVersion: 1, Type: "send_result", Channel: automation.Channel{Login: channel}, ObservedAt: time.Now().UTC(), Status: "rejected"}
	fail := func(code int, reason, message string) (automation.SendResult, int) {
		receipt.DropReason = &automation.DropReason{Code: reason, Message: message}
		receipt.ObservedAt = time.Now().UTC()
		return receipt, code
	}
	if action && !s.config.AllowAction {
		return fail(2, "invalid_input", "action unsupported by selected sender")
	}
	if action && utf8.RuneCountInString(text) > 491 {
		return fail(2, "invalid_input", "IRC action exceeds 491-character framed limit")
	}
	if len(reply) > 512 || textsafe.NeedsSanitizing(reply) {
		return fail(2, "invalid_input", "invalid reply identifier")
	}
	if err := ValidateText(text); err != nil {
		return fail(2, "invalid_input", err.Error())
	}
	selected, err := twitch.ParseChannel(channel)
	if err != nil {
		return fail(2, "invalid_channel", err.Error())
	}
	channel = selected.String()
	receipt.Channel.Login = channel
	if err := s.config.Auth.ValidateIfDue(ctx); err != nil {
		return fail(AuthErrorCode(err), "auth_scope_missing", err.Error())
	}
	id := s.channels[channel]
	if id == "" {
		lookup, cancel := context.WithTimeout(ctx, s.config.LookupTimeout)
		id, err = s.config.Resolve(lookup, channel)
		cancel()
		if err != nil || id == "" {
			return fail(4, "channel_unavailable", "cannot resolve channel")
		}
		if len(s.channels) >= 100 {
			clear(s.channels)
		}
		s.channels[channel] = id
	}
	receipt.Channel.ID = id
	if s.config.AuthorizeDestination != nil {
		bounded, cancel := context.WithTimeout(ctx, s.config.LookupTimeout)
		authErr := s.config.AuthorizeDestination(bounded, id)
		cancel()
		if authErr != nil {
			return fail(AuthErrorCode(authErr), "app_authorization_failed", authErr.Error())
		}
	}
	identity := s.config.Auth.Identity()
	role := sendquota.RoleNormal
	if id == identity.UserID && id != "" {
		role = sendquota.RoleBroadcaster
	} else if s.config.ModeratorProof != nil {
		bounded, cancel := context.WithTimeout(ctx, s.config.LookupTimeout)
		verified, proofErr := s.config.ModeratorProof(bounded, id)
		cancel()
		if proofErr == nil && verified {
			role = sendquota.RoleModerator
		}
	}
	var permitErr error
	if s.config.PermitRole != nil {
		permitErr = s.config.PermitRole(ctx, strings.ToLower(identity.Login), channel, role)
	} else {
		permitErr = s.config.Permit(ctx, strings.ToLower(identity.Login), channel)
	}
	if err := permitErr; err != nil {
		code, reason := 7, "quota_error"
		if errors.Is(err, sendquota.ErrLimited) {
			code, reason = 5, "rate_limited"
		}
		if ctx.Err() != nil {
			code = 4
		}
		return fail(code, reason, "send permit unavailable")
	}
	request := Request{BroadcasterID: id, SenderID: s.config.Auth.Identity().UserID, Text: text, Reply: reply, Channel: channel, Action: action}
	result, status, sendErr := s.config.Send(ctx, request)
	refreshFailure := 0
	if status == 401 && sendErr != nil {
		refresh, cancel := context.WithTimeout(ctx, s.config.RefreshTimeout)
		refreshErr := s.config.Auth.Refresh(refresh)
		cancel()
		if refreshErr == nil {
			request.SenderID = s.config.Auth.Identity().UserID
			result, status, sendErr = s.config.Send(ctx, request)
		} else {
			sendErr = refreshErr
			refreshFailure = AuthErrorCode(refreshErr)
		}
	}
	receipt.MessageID = result.MessageID
	receipt.ObservedAt = time.Now().UTC()
	receipt.Status = "sent"
	if sendErr != nil {
		failure := 4
		receipt.Status = "unknown"
		if status >= 400 && status < 500 {
			failure = 5
			receipt.Status = "rejected"
			if status == 401 || status == 403 {
				failure = 3
			}
		}
		if refreshFailure != 0 {
			failure = refreshFailure
			receipt.Status = "rejected"
		}
		return fail(failure, "send_"+receipt.Status, sendErr.Error())
	}
	if result.Unconfirmed {
		receipt.Status = "unconfirmed"
		return receipt, 0
	}
	if !result.Sent {
		receipt.Status = "rejected"
		receipt.DropReason = result.DropReason
		return receipt, 5
	}
	return receipt, 0
}
func AuthErrorCode(err error) int {
	if errors.Is(err, runtimeauth.ErrCredentialStore) {
		return 7
	}
	if errors.Is(err, twitch.ErrAuthFailed) {
		return 3
	}
	return 4
}

// ValidateText preserves message whitespace while enforcing the documented send
// bound and excluding controls that cannot form a single safe chat record.
func ValidateText(text string) error {
	if !utf8.ValidString(text) {
		return errors.New("message is not valid UTF-8")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message text cannot be empty")
	}
	if utf8.RuneCountInString(text) > twitch.MaxChatMessageRunes {
		return errors.New("message exceeds 500 characters")
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return errors.New("message contains forbidden control")
		}
	}
	return nil
}

// Identity returns the validated sender identity without credential material.
func (s *Service) Identity() twitch.TokenIdentity { return s.config.Auth.Identity() }

func (s *Service) Close() error {
	if s.config.Close != nil {
		return s.config.Close()
	}
	return nil
}

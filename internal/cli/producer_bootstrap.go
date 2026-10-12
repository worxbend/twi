package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/app"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/producer"
	"github.com/worxbend/twi/internal/runtimeapp"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/sendquota"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
	"github.com/worxbend/twi/internal/twitch/irc"
)

func newProducerDelivery(ctx context.Context, cfg config.Config, status credentialLoadStatus, o producerOptions) (*producer.Service, error) {
	if o.authMode == "app" {
		return newAppProducerDelivery(ctx, cfg, o)
	}
	required := []auth.Scope{auth.ScopeUserWriteChat}
	if o.transport == "irc" {
		required = []auth.Scope{auth.ScopeChatRead, auth.ScopeChatEdit}
	}
	authConfig := runtimeauth.Config{Credentials: twitch.TokenCredentials{Username: cfg.Twitch.Username, OAuthToken: cfg.Twitch.OAuthToken, ClientID: cfg.Twitch.ClientID, RefreshToken: cfg.Twitch.RefreshToken, ClientSecret: cfg.Twitch.ClientSecret}, Required: required}
	configureStoredRuntimeAuth(&authConfig, cfg, status)
	manager := runtimeauth.New(authConfig)
	setup, cancel := context.WithTimeout(ctx, o.connectTimeout)
	err := manager.Validate(setup)
	cancel()
	if err != nil {
		return nil, err
	}
	hc := helix.ClientConfig{ClientID: cfg.Twitch.ClientID, OAuthTokenSource: manager.Token, RequestTimeout: o.timeout}
	if o.rate == "fail" {
		hc.RetryPolicy = &helix.RetryPolicy{Wait: false}
	}
	sender, users := helix.NewSendChatClient(hc), helix.NewUsersClient(hc)
	roles := helix.NewModeratedChannelsClient(hc)
	var ircSender *irc.Producer
	if o.transport == "irc" {
		var err error
		ircSender, err = irc.NewProducer(ctx, irc.ProducerConfig{Login: manager.Identity().Login, Token: manager.Token})
		if err != nil {
			return nil, err
		}
	}
	return producer.New(producer.Config{AllowAction: o.transport == "irc", Close: func() error {
		if ircSender != nil {
			return ircSender.Close()
		}
		return nil
	}, ModeratorProof: func(ctx context.Context, broadcasterID string) (bool, error) {
		if !hasGrantedScope(manager.Scopes(), auth.Scope("user:read:moderated_channels")) {
			return false, nil
		}
		proof, err := roles.GetModeratedChannels(ctx, manager.Identity().UserID, []string{broadcasterID})
		return len(proof) == 1 && proof[0].BroadcasterID == broadcasterID, err
	}, Auth: manager, LookupTimeout: o.connectTimeout, RefreshTimeout: o.timeout,
		Resolve: func(ctx context.Context, channel string) (string, error) {
			resolved, err := users.GetUsers(ctx, twitch.UserLookupRequest{UserLogins: []string{channel}})
			if err != nil {
				return "", err
			}
			if len(resolved) != 1 {
				return "", errors.New("channel unavailable")
			}
			return resolved[0].UserID, nil
		},
		PermitRole: func(ctx context.Context, account, channel string, role sendquota.Role) error {
			return sendquota.AcquireForRole(ctx, account, channel, role, o.rate == "fail", o.interval)
		},
		Send: func(ctx context.Context, request producer.Request) (producer.Acknowledgement, int, error) {
			if ircSender != nil {
				bounded, cancel := context.WithTimeout(ctx, o.timeout)
				defer cancel()
				err := ircSender.Send(bounded, twitch.OutboundMessage{Channel: request.Channel, Text: request.Text, ReplyToMessageID: request.Reply, Action: request.Action})
				status := 0
				if errors.Is(err, twitch.ErrAuthFailed) {
					status = 401
				}
				return producer.Acknowledgement{Unconfirmed: err == nil}, status, err
			}
			result, status, err := sender.Send(ctx, helix.SendChatRequest{BroadcasterID: request.BroadcasterID, SenderID: request.SenderID, Message: request.Text, ReplyToMessageID: request.Reply})
			ack := producer.Acknowledgement{MessageID: result.MessageID, Sent: result.IsSent}
			if result.DropReason != nil {
				ack.DropReason = &automation.DropReason{Code: result.DropReason.Code, Message: result.DropReason.Message}
			}
			return ack, status, err
		},
	})
}

type acknowledgedSender struct {
	delivery *producer.Service
	mu       sync.Mutex
}

func (s *acknowledgedSender) Send(ctx context.Context, request app.SendRequest) (app.SendResult, error) {
	if request.Action {
		return app.SendResult{}, errors.New("acknowledged Helix sender does not support /me actions")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, code := s.delivery.Deliver(ctx, request.Channel, request.Text, request.ReplyToMessageID)
	result := app.SendResult{MessageID: receipt.MessageID, Detail: receipt.Status}
	if code == 0 {
		result.AcceptedAt = receipt.ObservedAt
		return result, nil
	}
	if receipt.DropReason != nil {
		result.Detail = receipt.DropReason.Message
	}
	if receipt.DropReason != nil && receipt.DropReason.Code == "rate_limited" {
		result.RateLimited = true
		return result, twitch.ErrRateLimited
	}
	return result, errors.New("Twitch send " + receipt.Status + ": " + config.RedactDisplayValue(result.Detail))
}
func (r *runner) newAcknowledgedSender(ctx context.Context, cfg config.Config) (*acknowledgedSender, error) {
	status, err := r.applyStoredCredentials(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	delivery, err := newProducerDelivery(ctx, cfg, status, producerOptions{dataOptions: dataOptions{connectTimeout: 30 * time.Second}, timeout: 15 * time.Second, rate: "wait"})
	if err != nil {
		return nil, err
	}
	return &acknowledgedSender{delivery: delivery}, nil
}

type historySendingSource struct {
	*app.HistoryChatSource
	*acknowledgedSender
}
type serviceSendingSource struct {
	*app.ServiceChatSource
	*acknowledgedSender
}
type ircSendingSource struct {
	*app.ServiceIRCClient
	sender *acknowledgedSender
}

func (s *ircSendingSource) Send(ctx context.Context, request app.SendRequest) (app.SendResult, error) {
	return s.sender.Send(ctx, request)
}

func newAppProducerDelivery(ctx context.Context, cfg config.Config, o producerOptions) (*producer.Service, error) {
	tokens := helix.NewAppTokenClient(helix.AppTokenConfig{ClientID: cfg.Twitch.ClientID, ClientSecret: auth.NewSecret(cfg.Twitch.ClientSecret)})
	manager := runtimeapp.New(runtimeapp.Config{Tokens: tokens})
	setup, cancel := context.WithTimeout(ctx, o.connectTimeout)
	defer cancel()
	if err := manager.ValidateIfDue(setup); err != nil {
		return nil, err
	}
	hc := helix.ClientConfig{ClientID: cfg.Twitch.ClientID, OAuthTokenSource: manager.Token, RequestTimeout: o.timeout}
	if o.rate == "fail" {
		hc.RetryPolicy = &helix.RetryPolicy{Wait: false}
	}
	users := helix.NewUsersClient(hc)
	sender, err := users.GetUsers(setup, twitch.UserLookupRequest{UserLogins: []string{o.senderLogin}})
	if err != nil {
		return nil, err
	}
	if len(sender) != 1 {
		return nil, fmt.Errorf("explicit app sender login unavailable: %w", twitch.ErrAuthFailed)
	}
	manager.SetSender(twitch.TokenIdentity{UserID: sender[0].UserID, Login: sender[0].Login})
	manager.SetAuthority(helix.NewAuthorizationClient(hc), helix.NewModeratedChannelsClient(hc))
	sendClient := helix.NewSendChatClient(hc)
	sourceOnly := o.sharedChat != "all"
	return producer.New(producer.Config{Auth: manager, AuthorizeDestination: manager.AuthorizeDestination, ModeratorProof: manager.ModeratorProof, LookupTimeout: o.connectTimeout, RefreshTimeout: o.timeout,
		Resolve: func(ctx context.Context, login string) (string, error) {
			items, err := users.GetUsers(ctx, twitch.UserLookupRequest{UserLogins: []string{login}})
			if err != nil {
				return "", err
			}
			if len(items) != 1 {
				return "", errors.New("channel unavailable")
			}
			return items[0].UserID, nil
		},
		PermitRole: func(ctx context.Context, account, channel string, role sendquota.Role) error {
			return sendquota.AcquireForRole(ctx, account, channel, role, o.rate == "fail", o.interval)
		},
		Send: func(ctx context.Context, request producer.Request) (producer.Acknowledgement, int, error) {
			result, status, err := sendClient.Send(ctx, helix.SendChatRequest{BroadcasterID: request.BroadcasterID, SenderID: request.SenderID, Message: request.Text, ReplyToMessageID: request.Reply, ForSourceOnly: &sourceOnly})
			ack := producer.Acknowledgement{MessageID: result.MessageID, Sent: result.IsSent}
			if result.DropReason != nil {
				ack.DropReason = &automation.DropReason{Code: result.DropReason.Code, Message: result.DropReason.Message}
			}
			return ack, status, err
		},
	})
}

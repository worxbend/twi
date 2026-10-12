package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/eventsub"
	"github.com/worxbend/twi/internal/twitch/irc"
	"os"
	"time"
)

func (r *runner) makeDataReader(ctx context.Context, o dataOptions, transport string, partial ...bool) (*chatservice.Service, error) {
	if transport == "eventsub" && len(o.channels) > eventsub.MaxReadChannels {
		return nil, fmt.Errorf("EventSub supports up to %d selected channels", eventsub.MaxReadChannels)
	}
	allowPartial := len(partial) > 0 && partial[0]
	if err := r.selectAccount(o.account); err != nil {
		return nil, err
	}
	cfg, err := loadDataConfig(o.configPath)
	if err != nil {
		return nil, dataLocalError{err}
	}
	credentialStatus, err := r.applyStoredCredentials(ctx, &cfg)
	if err != nil {
		return nil, dataLocalError{err}
	}
	if cfg.Twitch.OAuthToken == "" {
		return nil, fmt.Errorf("auth_scope_missing: missing credentials; run twi login --capability chat-read: %w", twitch.ErrAuthFailed)
	}
	required := []auth.Scope{auth.ScopeChatRead}
	if transport == "eventsub" {
		required = []auth.Scope{auth.Scope("user:read:chat")}
	}
	authConfig := runtimeauth.Config{Credentials: twitch.TokenCredentials{Username: cfg.Twitch.Username, OAuthToken: cfg.Twitch.OAuthToken, RefreshToken: cfg.Twitch.RefreshToken, ClientID: cfg.Twitch.ClientID, ClientSecret: cfg.Twitch.ClientSecret}, Required: required}
	configureStoredRuntimeAuth(&authConfig, cfg, credentialStatus)
	manager := runtimeauth.New(authConfig)
	factory := func(context.Context) (chatservice.Reader, error) {
		return irc.NewClient(headlessIRCConfig(manager, []string(o.channels)))
	}
	if transport == "eventsub" {
		factory, err = eventSubFactory(cfg, manager, []string(o.channels), allowPartial, o.connectTimeout)
		if err != nil {
			return nil, err
		}
	}
	readerFactory := factory
	factory = func(ctx context.Context) (chatservice.Reader, error) {
		if err := manager.ValidateIfDue(ctx); err != nil {
			return nil, err
		}
		return readerFactory(ctx)
	}
	return chatservice.New(chatservice.Config{Identity: manager.Identity, AllowPartial: allowPartial, Factory: factory, Channels: []string(o.channels), ConnectTimeout: o.connectTimeout, Validate: manager.Validate})
}
func isAuthDataError(err error) bool { return errors.Is(err, twitch.ErrAuthFailed) }

func loadDataConfig(path string) (config.Config, error) {
	return config.Load(os.Environ(), config.Overrides{ConfigPath: path})
}

func headlessIRCConfig(manager *runtimeauth.Manager, channels []string) irc.Config {
	credentials := manager.Credentials()
	return irc.Config{
		Username: manager.Identity().Login, OAuthToken: manager.Token(), RefreshToken: credentials.RefreshToken, ClientID: credentials.ClientID, ClientSecret: credentials.ClientSecret, Channels: channels,
		RefreshOAuth: func(ctx context.Context, at time.Time) (irc.OAuthRefresh, error) {
			if err := manager.Refresh(ctx); err != nil {
				return irc.OAuthRefresh{}, err
			}
			refreshed := manager.Credentials()
			return irc.OAuthRefresh{AccessToken: auth.NewSecret(twitch.NormalizeIRCOAuthToken(refreshed.OAuthToken)), RefreshToken: auth.NewSecret(refreshed.RefreshToken), RefreshedAt: at, RefreshTokenUpdated: refreshed.RefreshToken != credentials.RefreshToken}, nil
		},
	}
}

type dataLocalError struct{ error }

func (e dataLocalError) Unwrap() error { return e.error }
func dataErrorCode(err error) int {
	if isAuthDataError(err) {
		return 3
	}
	var local dataLocalError
	var path *os.PathError
	if errors.As(err, &local) || errors.As(err, &path) {
		return 7
	}
	return 4
}

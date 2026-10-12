package cli

import (
	"context"
	"errors"
	"github.com/worxbend/twi/internal/twitch/irc"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/twitch"
)

// configureStoredRuntimeAuth enables coordinated refresh only for the active saved token.
func configureStoredRuntimeAuth(authConfig *runtimeauth.Config, cfg config.Config, status credentialLoadStatus) {
	if status.Store == nil || !status.Present || status.AccessTokenShadowed {
		return
	}
	authConfig.Reload = func(ctx context.Context) (twitch.TokenCredentials, error) {
		record, ok, err := status.Store.LoadCredentials(ctx)
		if err != nil {
			return twitch.TokenCredentials{}, err
		}
		if !ok {
			return twitch.TokenCredentials{}, errors.New("saved credentials disappeared")
		}
		return twitch.TokenCredentials{Username: record.Login, OAuthToken: twitch.NormalizeIRCOAuthToken(record.AccessToken.Reveal()), RefreshToken: record.RefreshToken.Reveal(), ClientID: record.ClientID, ClientSecret: cfg.Twitch.ClientSecret}, nil
	}
	authConfig.Persist = func(ctx context.Context, credentials twitch.TokenCredentials) error {
		record, ok, err := status.Store.LoadCredentials(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("saved credentials disappeared")
		}
		record.AccessToken = auth.NewSecret(credentials.OAuthToken)
		record.RefreshToken = auth.NewSecret(credentials.RefreshToken)
		return status.Store.SaveCredentials(ctx, record)
	}
}

// sharedIRCRefresh keeps token rotation and persistence inside the account lock.
func sharedIRCRefresh(cfg config.Config, holder *credentialHolder, status credentialLoadStatus) (func(context.Context, time.Time) (irc.OAuthRefresh, error), func(context.Context, irc.OAuthRefresh) error, func(context.Context) error) {
	credentials := holder.current()
	authConfig := runtimeauth.Config{Credentials: twitch.TokenCredentials{Username: credentials.Username, OAuthToken: credentials.OAuthToken, RefreshToken: credentials.RefreshToken, ClientID: credentials.ClientID, ClientSecret: credentials.ClientSecret}, Required: auth.RequiredChatScopes()}
	configureStoredRuntimeAuth(&authConfig, cfg, status)
	var persistenceErr error
	var persistenceMu sync.Mutex
	persist := authConfig.Persist
	authConfig.Persist = func(ctx context.Context, credentials twitch.TokenCredentials) error {
		refreshed := irc.OAuthRefresh{AccessToken: auth.NewSecret(credentials.OAuthToken), RefreshToken: auth.NewSecret(credentials.RefreshToken)}
		holder.applyRefresh(refreshed)
		if persist != nil {
			err := persist(ctx, credentials)
			persistenceMu.Lock()
			persistenceErr = err
			persistenceMu.Unlock()
		}
		// IRC already reports persistence failures without abandoning the active token.
		return nil
	}
	manager := runtimeauth.New(authConfig)
	refresh := func(ctx context.Context, at time.Time) (irc.OAuthRefresh, error) {
		if err := manager.Refresh(ctx); err != nil {
			return irc.OAuthRefresh{}, err
		}
		credentials := manager.Credentials()
		refreshed := irc.OAuthRefresh{AccessToken: auth.NewSecret(credentials.OAuthToken), RefreshToken: auth.NewSecret(credentials.RefreshToken), RefreshedAt: at, RefreshTokenUpdated: true}
		holder.applyRefresh(refreshed)
		return refreshed, nil
	}
	callback := func(_ context.Context, refreshed irc.OAuthRefresh) error {
		holder.applyRefresh(refreshed)
		persistenceMu.Lock()
		err := persistenceErr
		persistenceErr = nil
		persistenceMu.Unlock()
		return err
	}
	return refresh, callback, manager.Validate
}

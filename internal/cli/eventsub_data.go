package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/runtimeauth"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/eventsub"
	"github.com/worxbend/twi/internal/twitch/helix"
)

func eventSubFactory(cfg config.Config, manager *runtimeauth.Manager, channels []string, allowPartial bool, connectTimeout time.Duration) (func(context.Context) (chatservice.Reader, error), error) {
	lookup := helix.NewUsersClient(helix.UsersClientConfig{ClientID: cfg.Twitch.ClientID, OAuthTokenSource: manager.Token})
	return func(ctx context.Context) (chatservice.Reader, error) {
		users, err := lookup.GetUsers(ctx, twitch.UserLookupRequest{UserLogins: channels})
		if err != nil {
			return nil, err
		}
		resolved, excluded, err := resolveEventSubChannels(channels, users, allowPartial)
		if err != nil {
			return nil, err
		}
		roles := helix.NewModeratedChannelsClient(helix.ClientConfig{ClientID: cfg.Twitch.ClientID, OAuthTokenSource: manager.Token})
		moderated, err := resolveModeratorChannels(ctx, roles, manager.Identity(), manager.Scopes(), resolved)
		if err != nil {
			return nil, err
		}
		return eventsub.NewClient(eventsub.Config{ModeratorChannels: moderated, Scopes: manager.Scopes(), ConnectTimeout: connectTimeout, AccountLogin: manager.Identity().Login, ClientID: cfg.Twitch.ClientID, UserID: manager.Identity().UserID, Token: manager.Token, Channels: resolved, ExcludedChannels: excluded})
	}, nil
}

func resolveEventSubChannels(channels []string, users []twitch.UserIdentity, partial bool) ([]eventsub.Channel, []string, error) {
	byLogin := make(map[string]string, len(users))
	for _, user := range users {
		byLogin[twitch.ChannelKey(user.Login)] = user.UserID
	}
	resolved := make([]eventsub.Channel, 0, len(channels))
	var excluded []string
	for _, channel := range channels {
		id := byLogin[twitch.ChannelKey(channel)]
		if id == "" {
			if !partial {
				return nil, nil, fmt.Errorf("channel_resolution_failed: %s", channel)
			}
			excluded = append(excluded, channel)
			continue
		}
		resolved = append(resolved, eventsub.Channel{Login: channel, ID: id})
	}
	if len(resolved) == 0 {
		return nil, nil, fmt.Errorf("channel_resolution_failed: no available selected channels")
	}
	return resolved, excluded, nil
}

func hasGrantedScope(scopes []auth.Scope, wanted auth.Scope) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

// resolveModeratorChannels treats Twitch's current role list as the authority.
// Scope grants permit lookup/subscription; they never establish a room role.
func resolveModeratorChannels(ctx context.Context, lookup twitch.ModeratedChannelLookup, identity twitch.TokenIdentity, scopes []auth.Scope, channels []eventsub.Channel) ([]string, error) {
	if !eventsub.CanReadModeration(scopes) || !hasGrantedScope(scopes, auth.Scope("user:read:moderated_channels")) {
		return nil, nil
	}
	selected := []string{}
	wanted := map[string]bool{}
	for _, channel := range channels {
		if channel.ID != identity.UserID && !wanted[channel.ID] {
			wanted[channel.ID] = true
			selected = append(selected, channel.ID)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	proof, err := lookup.GetModeratedChannels(ctx, identity.UserID, selected)
	if err != nil {
		return nil, err
	}
	result := []string{}
	seen := map[string]bool{}
	for _, channel := range proof {
		if wanted[channel.BroadcasterID] && !seen[channel.BroadcasterID] {
			seen[channel.BroadcasterID] = true
			result = append(result, channel.BroadcasterID)
		}
	}
	return result, nil
}

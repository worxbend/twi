package app

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

// channelMetricsPollInterval is longer than streamStatusPollInterval:
// follower/subscriber counts change far less often than LIVE status, and
// polling less often is kinder to Twitch's rate limits.
const channelMetricsPollInterval = 120 * time.Second

type channelMetricsTickMsg struct{}

type channelMetricsResolvedMsg struct {
	broadcasterID    string
	followers        twitch.FollowersPage
	followersErr     error
	subscriptions    twitch.SubscriptionsPage
	subscriptionsErr error
}

// scheduleChannelMetricsTick polls follower/subscriber counts on
// channelMetricsPollInterval. Disabled (both lookups nil) without live
// credentials or the relevant scopes.
func (m *shellModel) scheduleChannelMetricsTick() tea.Cmd {
	if (m.services.followerLookup == nil && m.services.subscriptionLookup == nil) || m.metrics.channelMetricsTickScheduled {
		return nil
	}
	m.metrics.channelMetricsTickScheduled = true
	return tea.Tick(channelMetricsPollInterval, func(time.Time) tea.Msg {
		return channelMetricsTickMsg{}
	})
}

func (m shellModel) resolveChannelMetricsCommand() tea.Cmd {
	if m.services.followerLookup == nil && m.services.subscriptionLookup == nil {
		return nil
	}
	followerLookup := m.services.followerLookup
	subscriptionLookup := m.services.subscriptionLookup
	userLookup := m.services.userLookup
	username := m.effectiveConfig.Twitch.Username
	knownID := m.selfBroadcasterID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		id := knownID
		if id == "" {
			resolved, err := resolveSelfBroadcasterID(ctx, userLookup, username)
			if err != nil {
				return channelMetricsResolvedMsg{followersErr: err, subscriptionsErr: err}
			}
			id = resolved
		}

		msg := channelMetricsResolvedMsg{broadcasterID: id}
		if followerLookup != nil {
			msg.followers, msg.followersErr = followerLookup.GetChannelFollowers(ctx, id, 25)
		}
		if subscriptionLookup != nil {
			msg.subscriptions, msg.subscriptionsErr = subscriptionLookup.GetBroadcasterSubscriptions(ctx, id, 1)
		}
		return msg
	}
}

func (m shellModel) applyChannelMetrics(msg channelMetricsResolvedMsg) shellModel {
	if msg.broadcasterID != "" {
		m.selfBroadcasterID = msg.broadcasterID
	}
	if m.services.followerLookup != nil && msg.followersErr == nil {
		m.metrics.followerCount = msg.followers.Total
		m.metrics.followerCountKnown = true
		m.applyNewFollowerActivity(msg.followers.Followers)
		// Followers are polled per broadcaster, so only the active channel's
		// roster can be annotated from this page.
		m.activeChannelState().roster.applyFollowers(msg.followers.Followers)
	}
	if m.services.subscriptionLookup != nil && msg.subscriptionsErr == nil {
		m.metrics.subscriberCount = msg.subscriptions.Total
		m.metrics.subscriberCountKnown = true
	}
	return m
}

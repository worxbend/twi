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
	if (m.followerLookup == nil && m.subscriptionLookup == nil) || m.channelMetricsTickScheduled {
		return nil
	}
	m.channelMetricsTickScheduled = true
	return tea.Tick(channelMetricsPollInterval, func(time.Time) tea.Msg {
		return channelMetricsTickMsg{}
	})
}

func (m shellModel) resolveChannelMetricsCommand() tea.Cmd {
	if m.followerLookup == nil && m.subscriptionLookup == nil {
		return nil
	}
	followerLookup := m.followerLookup
	subscriptionLookup := m.subscriptionLookup
	userLookup := m.userLookup
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
	if m.followerLookup != nil && msg.followersErr == nil {
		m.followerCount = msg.followers.Total
		m.followerCountKnown = true
		m.applyNewFollowerActivity(msg.followers.Followers)
		// Followers are polled per broadcaster, so only the active channel's
		// roster can be annotated from this page.
		m.activeChannelState().roster.applyFollowers(msg.followers.Followers)
	}
	if m.subscriptionLookup != nil && msg.subscriptionsErr == nil {
		m.subscriberCount = msg.subscriptions.Total
		m.subscriberCountKnown = true
	}
	return m
}

package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

type appFakeFollowerLookup struct {
	page twitch.FollowersPage
	err  error
}

func (f *appFakeFollowerLookup) GetChannelFollowers(context.Context, string, int) (twitch.FollowersPage, error) {
	return f.page, f.err
}

type appFakeSubscriptionLookup struct {
	page twitch.SubscriptionsPage
	err  error
}

func (f *appFakeSubscriptionLookup) GetBroadcasterSubscriptions(context.Context, string, int) (twitch.SubscriptionsPage, error) {
	return f.page, f.err
}

func TestChannelMetricsResolvesFollowerAndSubscriberCounts(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.selfBroadcasterID = "123"
	model.followerLookup = &appFakeFollowerLookup{page: twitch.FollowersPage{Total: 42, Followers: []twitch.Follower{
		{UserID: "1", UserName: "Viewer1"},
	}}}
	model.subscriptionLookup = &appFakeSubscriptionLookup{page: twitch.SubscriptionsPage{Total: 7, Points: 9}}

	cmd := model.resolveChannelMetricsCommand()
	if cmd == nil {
		t.Fatal("resolveChannelMetricsCommand returned nil, want a command")
	}
	msg := cmd().(channelMetricsResolvedMsg)
	model = model.applyChannelMetrics(msg)

	if !model.followerCountKnown || model.followerCount != 42 {
		t.Fatalf("followerCount = %d known=%v, want 42/true", model.followerCount, model.followerCountKnown)
	}
	if !model.subscriberCountKnown || model.subscriberCount != 7 {
		t.Fatalf("subscriberCount = %d known=%v, want 7/true", model.subscriberCount, model.subscriberCountKnown)
	}

	line := model.formatStatusMetrics(model.metricsNow(), false)
	if !strings.Contains(line, "followers=42") || !strings.Contains(line, "subs=7") {
		t.Fatalf("status metrics = %q, want followers=42 and subs=7", line)
	}
}

func TestChannelMetricsNilLookupsSkipNetwork(t *testing.T) {
	model := newMockModel("example", config.Default())
	if cmd := model.resolveChannelMetricsCommand(); cmd != nil {
		t.Fatal("resolveChannelMetricsCommand with no lookups = non-nil, want nil")
	}
	if cmd := model.scheduleChannelMetricsTick(); cmd != nil {
		t.Fatal("scheduleChannelMetricsTick with no lookups = non-nil, want nil")
	}
}

func TestChannelMetricsErrorLeavesCountsUnknown(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.selfBroadcasterID = "123"
	model.followerLookup = &appFakeFollowerLookup{err: errors.New("twitch says no")}
	model.subscriptionLookup = &appFakeSubscriptionLookup{err: errors.New("twitch says no")}

	msg := model.resolveChannelMetricsCommand()().(channelMetricsResolvedMsg)
	model = model.applyChannelMetrics(msg)
	if model.followerCountKnown || model.subscriberCountKnown {
		t.Fatalf("counts known after error = follower:%v sub:%v, want both false", model.followerCountKnown, model.subscriberCountKnown)
	}
}

func TestChannelMetricsTickReschedulesAndResolvesAgain(t *testing.T) {
	model := newMockModel("example", config.Default())
	model.selfBroadcasterID = "123"
	model.followerLookup = &appFakeFollowerLookup{page: twitch.FollowersPage{Total: 1}}

	cmd := model.scheduleChannelMetricsTick()
	if cmd == nil {
		t.Fatal("scheduleChannelMetricsTick returned nil, want a tick command")
	}
	if !model.channelMetricsTickScheduled {
		t.Fatal("channelMetricsTickScheduled = false after scheduling, want true")
	}
	if again := model.scheduleChannelMetricsTick(); again != nil {
		t.Fatal("scheduleChannelMetricsTick returned non-nil while already scheduled, want nil")
	}

	updated, batchCmd := model.Update(channelMetricsTickMsg{})
	model = updated.(shellModel)
	if !model.channelMetricsTickScheduled {
		t.Fatal("channelMetricsTickScheduled = false after tick fired and rescheduled, want true")
	}
	if batchCmd == nil {
		t.Fatal("Update(channelMetricsTickMsg{}) returned nil command")
	}
}

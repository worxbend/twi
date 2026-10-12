package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/twitch"
)

func echoRetentionModel() shellModel {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.Features.ScrollbackLimit = 32
	cfg.DefaultChannels = []string{"alpha"}
	return newShellModel("alpha", cfg, nil)
}
func TestLocalEchoTrackingRemainsBounded(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(fmt.Sprintf("acknowledged=%v", acknowledged), func(t *testing.T) {
			model := echoRetentionModel()
			for index := range 1000 {
				result := SendResult{QueuedAt: time.Now()}
				if acknowledged {
					result.MessageID = fmt.Sprintf("accepted-%d", index)
					result.AcceptedAt = result.QueuedAt
				}
				model.appendLocalEcho(queuedComposerSend{ID: index, Channel: "alpha", Text: "sent body"}, result)
			}
			state := model.activeChannelState()
			maximum := 32 + scrollbackTrimMargin(32)
			if len(state.messages) > maximum || len(state.localEchoes) > maximum {
				t.Fatalf("retention exceeded: messages=%d echo IDs=%d maximum=%d", len(state.messages), len(state.localEchoes), maximum)
			}
			if !acknowledged && len(state.localEchoes) != 0 {
				t.Fatal("synthetic IRC IDs registered as expected Twitch echoes")
			}
			model.clearLocalChat()
			if len(state.localEchoes) != 0 {
				t.Fatal("clear retained echo tracking")
			}
		})
	}
}
func TestObservedSelfMessageWinsRegardlessOfAcknowledgementOrder(t *testing.T) {
	for _, observedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("observed-first=%v", observedFirst), func(t *testing.T) {
			model := echoRetentionModel()
			sent := queuedComposerSend{ID: 1, Channel: "alpha", Text: "hello"}
			result := SendResult{MessageID: "accepted-id", AcceptedAt: time.Now()}
			observed := twitch.ChatMessage{ID: "accepted-id", Channel: "alpha", AuthorLogin: "authenticated-user", Text: "hello", Timestamp: result.AcceptedAt, Type: twitch.MessageTypeChat, SourceChannelID: "source-id"}
			if observedFirst {
				model.ingestMessage(observed)
				model.appendLocalEcho(sent, result)
			} else {
				model.appendLocalEcho(sent, result)
				model.ingestMessage(observed)
			}
			state := model.activeChannelState()
			if len(state.messages) != 1 || state.messages[0].SourceChannelID != "source-id" || len(state.localEchoes) != 0 {
				t.Fatalf("self message duplicated or observed data lost: messages=%+v echoes=%v", state.messages, state.localEchoes)
			}
		})
	}
}

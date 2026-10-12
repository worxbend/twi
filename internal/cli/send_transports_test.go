package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/producer"
	"github.com/worxbend/twi/internal/twitch"
)

type transportAuth struct{}

func (transportAuth) ValidateIfDue(context.Context) error { return nil }
func (transportAuth) Refresh(context.Context) error       { return nil }
func (transportAuth) Identity() twitch.TokenIdentity {
	return twitch.TokenIdentity{UserID: "sender", Login: "bot"}
}
func TestIRCAutomationActionUsesUnconfirmedOutcome(t *testing.T) {
	t.Setenv("TWI_TWITCH_CLIENT_ID", "fake-client")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "fake-token")
	runner := newRunner()
	requests := 0
	runner.producerDelivery = func(_ context.Context, _ config.Config, _ credentialLoadStatus, options producerOptions) (*producer.Service, error) {
		if options.transport != "irc" || !options.action || options.authMode != "user" {
			t.Fatalf("wrong transport options=%+v", options)
		}
		return producer.New(producer.Config{Auth: transportAuth{}, AllowAction: true, Resolve: func(context.Context, string) (string, error) { return "room", nil }, Permit: func(context.Context, string, string) error { return nil }, Send: func(_ context.Context, request producer.Request) (producer.Acknowledgement, int, error) {
			requests++
			if request.Text != " waves " || !request.Action || request.Channel != "alpha" {
				t.Fatalf("changed action=%+v", request)
			}
			return producer.Acknowledgement{Unconfirmed: true}, 0, nil
		}})
	}
	var stdout, stderr bytes.Buffer
	code := runner.RunContext(context.Background(), []string{"produce", "-t", "alpha", "--transport", "irc", "--action", "-m", " waves "}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || requests != 1 {
		t.Fatalf("code=%d sends=%d stderr=%s", code, requests, stderr.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["status"] != "unconfirmed" || receipt["message_id"] != "" || receipt["transport"] != "irc" {
		t.Fatalf("false acceptance receipt=%v", receipt)
	}
}
func TestAppSendIsolationAndActionConflictsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"produce", "-t", "alpha", "--auth", "app", "-m", "hi"}, {"produce", "-t", "alpha", "--sender", "bot", "-m", "hi"}, {"produce", "-t", "alpha", "--shared-chat", "source", "-m", "hi"}, {"produce", "-t", "alpha", "--auth", "app", "--sender", "bot", "--transport", "irc", "-m", "hi"}, {"produce", "-t", "alpha", "--action", "-m", "hi"}} {
		var stdout, stderr bytes.Buffer
		runner := newRunner()
		runner.producerDelivery = func(context.Context, config.Config, credentialLoadStatus, producerOptions) (*producer.Service, error) {
			t.Fatal("network factory reached on usage error")
			return nil, nil
		}
		if code := runner.RunContext(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("args=%v code=%d error=%s", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := RunContext(context.Background(), []string{"produce", "-t", "alpha", "--auth", "app", "--sender", "bot", "--dry-run", "-m", "hello"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("app dry-run=%d %s", code, stderr.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["for_source_only"] != true || receipt["authorization"] != "app" || receipt["status"] != "validated" {
		t.Fatalf("isolation receipt=%v", receipt)
	}
}

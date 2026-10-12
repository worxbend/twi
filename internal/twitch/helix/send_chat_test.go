package helix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendChatAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name, body string
		sent       bool
		invalid    bool
	}{{"accepted", `{"data":[{"message_id":"42","is_sent":true,"drop_reason":null}]}`, true, false}, {"dropped", `{"data":[{"message_id":"","is_sent":false,"drop_reason":{"code":"blocked","message":"blocked"}}]}`, false, false}, {"missing", `{"data":[]}`, false, true}, {"empty_id", `{"data":[{"is_sent":true}]}`, false, true}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("wrong authenticated request")
				}
				var input SendChatRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input.Message != "  preserved  " || input.ReplyToMessageID != "parent" {
					t.Errorf("request %+v", input)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := NewSendChatClient(ClientConfig{Endpoint: server.URL, OAuthToken: "oauth:secret"})
			result, status, err := client.Send(context.Background(), SendChatRequest{BroadcasterID: "123", SenderID: "456", Message: "  preserved  ", ReplyToMessageID: "parent"})
			if (err != nil) != test.invalid || result.IsSent != test.sent || status != 200 || calls != 1 {
				t.Fatalf("result %+v status %d err %v calls %d", result, status, err, calls)
			}
		})
	}
}
func TestSendChatNeverRetriesServerFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer server.Close()
	_, status, err := NewSendChatClient(ClientConfig{Endpoint: server.URL}).Send(context.Background(), SendChatRequest{Message: "hello"})
	if err == nil || status != 503 || calls != 1 {
		t.Fatalf("status %d err %v calls %d", status, err, calls)
	}
}

func TestSendDropReasonRedactsCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"is_sent":false,"drop_reason":{"code":"blocked","message":"secret OAuth access-token"}}]}`))
	}))
	defer server.Close()
	result, _, err := NewSendChatClient(ClientConfig{Endpoint: server.URL, OAuthToken: "oauth:secret"}).Send(context.Background(), SendChatRequest{Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.DropReason == nil || strings.Contains(result.DropReason.Message, "secret") {
		t.Fatalf("unsafe reason %+v", result.DropReason)
	}
}

func TestSharedChatParameterAbsentForUserAndExplicitForApp(t *testing.T) {
	t.Parallel()
	for _, value := range []*bool{nil, func() *bool { v := true; return &v }(), func() *bool { v := false; return &v }()} {
		payload, err := json.Marshal(SendChatRequest{BroadcasterID: "room", SenderID: "sender", Message: "hi", ForSourceOnly: value})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		actual, present := fields["for_source_only"]
		if value == nil && present {
			t.Fatal("user-token body includes forbidden source override")
		}
		if value != nil && (!present || actual != *value) {
			t.Fatalf("app isolation flag lost: %s", payload)
		}
	}
}

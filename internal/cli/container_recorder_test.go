package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/twitch"
)

type containerRecorderReader struct{}

func (containerRecorderReader) Close() error { return nil }
func (containerRecorderReader) Connect(ctx context.Context) (<-chan twitch.Event, error) {
	events := make(chan twitch.Event, 8)
	go func() {
		defer close(events)
		events <- twitch.Event{Kind: twitch.EventRoomState, RoomState: twitch.RoomState{Channel: "alpha"}}
		events <- twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: "outside-selection", Channel: "beta", Text: "must not enter alpha history"}}
		events <- twitch.Event{Kind: twitch.EventMessage, Message: twitch.ChatMessage{ID: "fixture-message", Channel: "alpha", Text: "offline packaging fixture"}}
		<-ctx.Done()
	}()
	return events, nil
}

// Explicit opt-in packaging helper. The real foreground recorder command runs
// with a per-invocation scripted transport; no Twitch credentials or network.
func TestContainerRecorderHelper(t *testing.T) {
	mode := os.Getenv("TWI_CONTAINER_RECORDER_MODE")
	if mode == "" {
		t.Skip("container packaging helper")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	r := newRunner()
	r.recordReader = func(context.Context, dataOptions, string) (*chatservice.Service, error) {
		return chatservice.New(chatservice.Config{Channels: []string{"alpha"}, Factory: func(context.Context) (chatservice.Reader, error) { return containerRecorderReader{}, nil }})
	}
	path := "/state/private/history.db"
	if mode == "permissions" {
		path = "/readonly/history.db"
	}
	if mode != "permissions" {
		go func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					store, err := history.Open(path)
					if err != nil {
						continue
					}
					rows, _, err := store.ReadAfter(ctx, history.Query{}, 0, 10)
					store.Close()
					if err == nil && len(rows) > 0 {
						fmt.Println("recorder ready")
						return
					}
				}
			}
		}()
	}
	code := r.runRecord(ctx, []string{"--channel", "alpha", "--history", path}, os.Stdout, os.Stderr)
	if mode == "permissions" {
		if code != 7 {
			t.Fatalf("permission failure exit=%d", code)
		}
		fmt.Println("permission rejection passed")
		return
	}
	if ctx.Err() == nil || code != 0 {
		t.Fatalf("signal did not stop recorder cleanly: code=%d canceled=%t", code, ctx.Err() != nil)
	}
	store, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, _, err := store.ReadAfter(context.Background(), history.Query{}, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "fixture-message" {
		t.Fatal("shutdown lost observed event", rows, err)
	}
	status, err := store.Status(context.Background())
	if err != nil || len(status.Captures) != 1 || !status.Captures[0].Clean || !status.PrivateFiles {
		t.Fatal(status, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database permissions", info, err)
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil || directory.Mode().Perm() != 0700 {
		t.Fatal("state permissions", directory, err)
	}
	fmt.Println("signal flush and private state passed")
}

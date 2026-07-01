package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/w0rxbend/twi/internal/app"
	"github.com/w0rxbend/twi/internal/config"
)

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "twi chat") {
		t.Fatalf("help output missing chat command: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestMockChat(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"chat", "--mock", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#example") {
		t.Fatalf("mock chat output missing channel: %q", stdout.String())
	}
}

func TestLiveChatMissingCredentialsAreActionableAndRedacted(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"TWI_TWITCH_USERNAME", "chat:read", "chat:edit", "--mock"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "oauth:secret-token") {
		t.Fatalf("stderr leaked token: %q", stderr.String())
	}
}

func TestLiveChatConfiguredStartsClient(t *testing.T) {
	t.Setenv("TWI_TWITCH_USERNAME", "viewer")
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret-token")

	oldNewLiveChatClient := newLiveChatClient
	oldRunLiveChat := runLiveChat
	defer func() {
		newLiveChatClient = oldNewLiveChatClient
		runLiveChat = oldRunLiveChat
	}()

	var gotChannels []string
	fake := app.NewFakeChatClient(1)
	newLiveChatClient = func(_ context.Context, cfg config.Config) (app.ChatClient, error) {
		gotChannels = append([]string(nil), cfg.DefaultChannels...)
		return fake, nil
	}
	runLiveChat = func(stdout io.Writer, cfg config.Config, client app.ChatClient) error {
		if client != fake {
			t.Fatalf("runLiveChat client = %#v, want fake", client)
		}
		_, err := stdout.Write([]byte("live shell started\n"))
		return err
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"chat", "--config", t.TempDir() + "/missing.toml", "--channel", "example"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "oauth:secret-token") {
		t.Fatalf("stderr leaked token: %q", stderr.String())
	}
	if got, want := strings.Join(gotChannels, ","), "example"; got != want {
		t.Fatalf("factory channels = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "live shell started") {
		t.Fatalf("stdout missing live shell output: %q", stdout.String())
	}
}

func TestConfigShowRedactsSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret")
	t.Setenv("TWI_TWITCH_CLIENT_SECRET", "client-secret")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"config", "show", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, secret := range []string{"oauth:secret", "client-secret"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("config output leaked %q: %s", secret, stdout.String())
		}
	}
}

func TestDoctorDoesNotPrintSecrets(t *testing.T) {
	t.Setenv("TWI_TWITCH_OAUTH_TOKEN", "oauth:secret")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "--config", t.TempDir() + "/missing.toml"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "oauth:secret") {
		t.Fatalf("doctor output leaked token: %s", stdout.String())
	}
}

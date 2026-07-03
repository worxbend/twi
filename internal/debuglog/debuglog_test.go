package debuglog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/w0rxbend/twi/internal/auth"
)

func TestDisabledLoggerWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, Options{})

	logger.Log(context.Background(), "test", slog.String("token", "oauth:secret"))

	if buf.Len() != 0 {
		t.Fatalf("disabled logger wrote %q, want empty", buf.String())
	}
}

func TestLoggerRedactsSecretsAndAvoidsRawAnyDumps(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, Options{
		Enabled: true,
		Secrets: []auth.Secret{
			auth.NewSecret("plain-refresh-secret"),
			auth.NewSecret("client-secret"),
		},
	})

	logger.Log(context.Background(), "redaction.test",
		slog.String("oauth_token", "oauth:access-secret"),
		slog.String("refresh", "plain-refresh-secret"),
		slog.String("auth_header", "Authorization: Bearer bearer-secret"),
		slog.String("callback", "http://127.0.0.1/callback?code=callback-code&state=state-secret&client_secret=client-secret"),
		slog.String("source_url", "https://cdn.example/emote.png?access_token=source-secret"),
		slog.String("userinfo_url", "open https://user:password@example.com/path"),
		slog.Any("raw_struct", struct {
			Token string
		}{Token: "oauth:any-secret"}),
	)

	output := buf.String()
	for _, secret := range []string{
		"oauth:access-secret",
		"access-secret",
		"plain-refresh-secret",
		"bearer-secret",
		"callback-code",
		"state-secret",
		"client-secret",
		"source-secret",
		"user:password",
		"oauth:any-secret",
		"any-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("debug log leaked %q:\n%s", secret, output)
		}
	}
	for _, want := range []string{`"event":"redaction.test"`, Redacted, `"raw_struct":"<struct { Token string }>"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func TestURLFieldsDoNotIncludeRawSourceURL(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, Options{Enabled: true})

	logger.Log(context.Background(), "url.test", URLFields("source_url", "https://cdn.example/image.png?access_token=source-secret")...)

	output := buf.String()
	if strings.Contains(output, "source-secret") || strings.Contains(output, "image.png?") {
		t.Fatalf("URL fields leaked raw source URL:\n%s", output)
	}
	for _, want := range []string{`"source_url_scheme":"https"`, `"source_url_host":"cdn.example"`, `"source_url_has_credential_marker":true`} {
		if !strings.Contains(output, want) {
			t.Fatalf("URL fields missing %q:\n%s", want, output)
		}
	}
}

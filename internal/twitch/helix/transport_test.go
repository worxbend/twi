package helix

import (
	"net/url"
	"testing"
)

func TestClampLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "unset falls back", limit: 0, want: 20},
		{name: "negative falls back", limit: -5, want: 20},
		{name: "in range is kept", limit: 7, want: 7},
		{name: "maximum is kept", limit: 100, want: 100},
		{name: "above maximum is lowered", limit: 4000, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampLimit(tt.limit, 20, 100); got != tt.want {
				t.Fatalf("clampLimit(%d, 20, 100) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

func TestQueryURLSetsParameters(t *testing.T) {
	got, err := queryURL("https://api.twitch.tv/helix/clips", url.Values{
		"broadcaster_id": {"12345"},
		"first":          {"20"},
	})
	if err != nil {
		t.Fatalf("queryURL returned error: %v", err)
	}
	const want = "https://api.twitch.tv/helix/clips?broadcaster_id=12345&first=20"
	if got != want {
		t.Fatalf("queryURL = %q, want %q", got, want)
	}
}

// A test server's endpoint can already carry a query string, so the values the
// adapter sets have to replace same-named parameters rather than pile up
// beside them.
func TestQueryURLReplacesExistingParameter(t *testing.T) {
	got, err := queryURL("https://example.test/helix?first=1&trace=on", url.Values{
		"first": {"50"},
	})
	if err != nil {
		t.Fatalf("queryURL returned error: %v", err)
	}
	const want = "https://example.test/helix?first=50&trace=on"
	if got != want {
		t.Fatalf("queryURL = %q, want %q", got, want)
	}
}

func TestQueryURLRejectsMalformedEndpoint(t *testing.T) {
	if _, err := queryURL("://nonsense", url.Values{"first": {"1"}}); err == nil {
		t.Fatal("queryURL accepted a malformed endpoint, want an error")
	}
}

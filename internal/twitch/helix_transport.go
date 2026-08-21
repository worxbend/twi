package twitch

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// helixTransport carries the HTTP plumbing every Helix adapter in this
// package shares: the HTTP client to send on, the registered application's
// Client-Id, and the accessor for the current OAuth token.
//
// Each adapter used to declare those three fields itself and then repeat the
// same token(), setAuthHeaders() and non-2xx error handling - nine
// near-identical copies of each. Embedding one value gives them a single
// definition, so changing how twi authenticates to Helix, or how it reports a
// Helix failure, is one edit instead of nine.
type helixTransport struct {
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

// newHelixTransport builds the shared plumbing from the fields every Helix
// client config carries. A nil httpClient falls back to http.DefaultClient so
// a zero-valued config still produces a usable client.
func newHelixTransport(httpClient *http.Client, clientID string, tokenSource func() string, staticToken string) helixTransport {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return helixTransport{
		httpClient:       httpClient,
		clientID:         strings.TrimSpace(clientID),
		oauthTokenSource: resolveTokenSource(tokenSource, staticToken),
	}
}

// endpointOrDefault returns the configured endpoint, or fallback when the
// config left it blank. Every Helix client resolves its endpoint this way, so
// pointing one at a test server is a matter of setting its config field.
func endpointOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// token returns the OAuth token to present on the next request, read now
// rather than captured at construction so a mid-session refresh applies.
func (t helixTransport) token() string {
	if t.oauthTokenSource == nil {
		return ""
	}
	return t.oauthTokenSource()
}

// setAuthHeaders adds the two headers Helix authenticates with. Either is
// left off when it is unset, so Twitch answers with its own explanation
// rather than twi guessing at one.
func (t helixTransport) setAuthHeaders(req *http.Request) {
	if t.clientID != "" {
		req.Header.Set("Client-Id", t.clientID)
	}
	if token := accessTokenForValidation(t.token()); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// newGetRequest builds an authenticated Helix GET for endpoint. action names
// what the caller was doing, so a construction failure reads as, for
// instance, "create Twitch user lookup request: ...".
func (t helixTransport) newGetRequest(ctx context.Context, endpoint, action string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, credentialSafeUserError(action, err, t.token())
	}
	t.setAuthHeaders(req)
	return req, nil
}

// helixErrorLabels names the strings that make up a Helix failure message.
//
// They are grouped rather than passed as three loose strings because all
// three describe the same call, and three adjacent string parameters are easy
// to transpose at a call site without the compiler noticing.
type helixErrorLabels struct {
	// action describes what twi was attempting, e.g. "lookup Twitch users".
	action string
	// readAction describes reading the error body back, e.g. "read Twitch
	// user lookup response". It appears only when that read itself fails.
	readAction string
	// endpoint is Twitch's documented name for the endpoint, e.g. "Get
	// Users", quoted verbatim so a reader can find it in the Helix docs.
	endpoint string
	// channelAPIStatuses lists the status codes to wrap in a
	// ChannelAPIError. Callers that let their own callers branch on the
	// status - to tell "your token lacks a scope" from "that channel does
	// not exist" - name those codes here; the rest leave it empty.
	channelAPIStatuses []int
}

// responseError turns a non-2xx Helix response into an error whose message is
// safe to display, reading a bounded amount of the body for detail.
func (t helixTransport) responseError(resp *http.Response, labels helixErrorLabels) error {
	detail, err := readSmallBody(resp.Body)
	if err != nil {
		return credentialSafeUserError(labels.readAction, err, t.token())
	}
	if detail != "" {
		detail = ": " + detail
	}
	wrapped := credentialSafeUserError(
		labels.action,
		fmt.Errorf("twitch %s returned HTTP %d%s", labels.endpoint, resp.StatusCode, detail),
		t.token(),
	)
	if slices.Contains(labels.channelAPIStatuses, resp.StatusCode) {
		return &ChannelAPIError{StatusCode: resp.StatusCode, err: wrapped}
	}
	return wrapped
}

// isHelixSuccess reports whether a Helix response carries a 2xx status.
func isHelixSuccess(resp *http.Response) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// HelixClientConfig configures a Helix adapter.
//
// Every adapter in this package takes exactly these settings, so there is one
// struct and the per-adapter names below are aliases for it. The struct used
// to be declared nine times, identically down to its doc comment, which meant
// nine edits to add a setting.
type HelixClientConfig struct {
	// OAuthTokenSource, when set, is read on every request so a token
	// refreshed mid-session takes effect. OAuthToken is the static fallback
	// used when no source is supplied.
	OAuthTokenSource func() string
	// Endpoint overrides the Twitch URL the adapter calls, which is how a
	// test points one at its own server. It defaults to the real endpoint.
	Endpoint   string
	HTTPClient *http.Client
	ClientID   string
	OAuthToken string
}

// The per-adapter configuration names. They are aliases rather than distinct
// types: the settings are the same for every adapter, and the names exist to
// make a construction site say which adapter it is building.
type (
	HelixChannelsClientConfig         = HelixClientConfig
	HelixClipsClientConfig            = HelixClientConfig
	HelixFollowedChannelsClientConfig = HelixClientConfig
	HelixFollowersClientConfig        = HelixClientConfig
	HelixGamesClientConfig            = HelixClientConfig
	HelixMarkersClientConfig          = HelixClientConfig
	HelixStreamsClientConfig          = HelixClientConfig
	HelixSubscriptionsClientConfig    = HelixClientConfig
	HelixUsersClientConfig            = HelixClientConfig
)

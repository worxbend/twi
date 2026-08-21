package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/worxbend/twi/internal/twitch"
)

// transport carries the HTTP plumbing every Helix adapter in this
// package shares: the HTTP client to send on, the registered application's
// Client-Id, and the accessor for the current OAuth token.
//
// Each adapter used to declare those three fields itself and then repeat the
// same token(), setAuthHeaders() and non-2xx error handling - nine
// near-identical copies of each. Embedding one value gives them a single
// definition, so changing how twi authenticates to Helix, or how it reports a
// Helix failure, is one edit instead of nine.
type transport struct {
	httpClient       *http.Client
	clientID         string
	oauthTokenSource func() string
}

// newTransport builds the shared plumbing from the fields every Helix
// client config carries. A nil httpClient falls back to http.DefaultClient so
// a zero-valued config still produces a usable client.
func newTransport(httpClient *http.Client, clientID string, tokenSource func() string, staticToken string) transport {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return transport{
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
func (t transport) token() string {
	if t.oauthTokenSource == nil {
		return ""
	}
	return t.oauthTokenSource()
}

// setAuthHeaders adds the two headers Helix authenticates with. Either is
// left off when it is unset, so Twitch answers with its own explanation
// rather than twi guessing at one.
func (t transport) setAuthHeaders(req *http.Request) {
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
func (t transport) newGetRequest(ctx context.Context, endpoint, action string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, credentialSafeUserError(action, err, t.token())
	}
	t.setAuthHeaders(req)
	return req, nil
}

// errorLabels names the strings that make up a Helix failure message.
//
// They are grouped rather than passed as three loose strings because all
// three describe the same call, and three adjacent string parameters are easy
// to transpose at a call site without the compiler noticing.
type errorLabels struct {
	// action describes what twi was attempting, e.g. "lookup Twitch users".
	action string
	// readAction describes reading the error body back, e.g. "read Twitch
	// user lookup response". It appears only when that read itself fails.
	readAction string
	// endpoint is Twitch's documented name for the endpoint, e.g. "Get
	// Users", quoted verbatim so a reader can find it in the Helix docs.
	endpoint string
	// channelAPIReasons maps the status codes worth telling apart to the
	// domain reason they mean. Adapters whose callers act on the difference
	// -- "your token lacks a scope" versus "that channel is not live" --
	// fill this in; the rest leave it empty and their failures arrive as
	// plain errors.
	channelAPIReasons map[int]twitch.ChannelAPIReason
}

// responseError turns a non-2xx Helix response into an error whose message is
// safe to display, reading a bounded amount of the body for detail.
func (t transport) responseError(resp *http.Response, labels errorLabels) error {
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
	if reason, ok := labels.channelAPIReasons[resp.StatusCode]; ok {
		return twitch.NewChannelAPIError(reason, resp.StatusCode, wrapped)
	}
	return wrapped
}

// isSuccess reports whether a Helix response carries a 2xx status.
func isSuccess(resp *http.Response) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// ClientConfig configures a Helix adapter.
//
// Every adapter in this package takes exactly these settings, so there is one
// struct and the per-adapter names below are aliases for it. The struct used
// to be declared nine times, identically down to its doc comment, which meant
// nine edits to add a setting.
type ClientConfig struct {
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
	ChannelsClientConfig         = ClientConfig
	ClipsClientConfig            = ClientConfig
	FollowedChannelsClientConfig = ClientConfig
	FollowersClientConfig        = ClientConfig
	GamesClientConfig            = ClientConfig
	MarkersClientConfig          = ClientConfig
	StreamsClientConfig          = ClientConfig
	SubscriptionsClientConfig    = ClientConfig
	UsersClientConfig            = ClientConfig
)

// getJSON performs one authenticated Helix GET and decodes the response.
//
// Every read-path adapter method ran this same sequence inline -- build the
// request, send it, check the status, decode the body -- with four
// credentialSafeUserError call sites apiece and eleven copies in total. The
// adapters are now only what actually differs between them: the URL to call,
// the labels to report a failure under, and mapping the decoded payload to a
// domain type.
//
// It is generic over the wire type so the caller gets a decoded value rather
// than passing a pointer into an out-parameter, which keeps the shape of every
// adapter method the same.
func getJSON[T any](ctx context.Context, t transport, endpoint string, labels errorLabels) (T, error) {
	var decoded T

	req, err := t.newGetRequest(ctx, endpoint, "create Twitch "+labels.subject()+" request")
	if err != nil {
		return decoded, err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return decoded, credentialSafeUserError(labels.action, err, t.token())
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return decoded, t.responseError(resp, labels)
	}
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return decoded, credentialSafeUserError("decode Twitch "+labels.subject()+" response", err, t.token())
	}
	return decoded, nil
}

// subject is the noun phrase shared by an endpoint's request and decode error
// messages, taken from readAction, which reads "read Twitch <subject>
// response". Deriving it keeps one label per endpoint rather than three that
// have to be kept consistent by hand.
func (l errorLabels) subject() string {
	subject := strings.TrimPrefix(l.readAction, "read Twitch ")
	return strings.TrimSuffix(subject, " response")
}

// credentialSafeUserError wraps err with a message safe to display.
//
// A cancelled or timed-out context is returned unchanged: that is the caller's
// own doing, and wrapping it would hide the sentinel the caller checks for.
// Everything else is redacted, because a Helix error body echoes back the
// request that produced it -- which is where the credentials are.
//
// This lived in users.go, and chat_assets.go carried a byte-identical second
// copy under a different name. It belongs with the rest of the shared
// transport code, which is what every adapter reports errors through.
func credentialSafeUserError(action string, err error, oauthToken string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	credentials := twitch.TokenCredentials{OAuthToken: oauthToken}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}

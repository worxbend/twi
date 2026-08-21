package helix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

// newJSONRequest builds an authenticated Helix request whose body is body
// encoded as a JSON document, which is how every Helix write sends its
// payload. labels supplies the two messages a failure before the request
// leaves twi is reported under: one for encoding the payload, one for building
// the request around it.
func (t transport) newJSONRequest(ctx context.Context, method, endpoint string, body any, labels writeLabels) (*http.Request, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, credentialSafeUserError(labels.encodeAction, err, t.token())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, credentialSafeUserError(labels.createAction, err, t.token())
	}
	req.Header.Set("Content-Type", "application/json")
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

// writeLabels names every message one Helix write can fail under.
//
// The read path derives its request and decode messages from a single label
// (see errorLabels.subject); the write path cannot, because the wording does
// not follow from one noun. Creating a stream marker, for instance, reports
// its own failures in the singular ("Twitch stream marker") while sharing the
// plural response labels of the marker *list* endpoint. Spelling the messages
// out keeps each one exactly what it has always been.
type writeLabels struct {
	// encodeAction covers turning the payload into JSON, e.g. "encode Twitch
	// clip request".
	encodeAction string
	// createAction covers building the HTTP request, e.g. "create Twitch clip
	// request".
	createAction string
	// decodeAction covers reading Twitch's answer back, e.g. "decode Twitch
	// clip response". A write with nothing to decode leaves it empty.
	decodeAction string
	// errorLabels covers sending the request and any non-2xx reply, exactly as
	// it does on the read path.
	errorLabels
}

// sendJSON performs one authenticated Helix write and decodes the response.
//
// It is the write-path twin of getJSON: the adapter method supplies the URL,
// the payload and the labels, and gets back a decoded wire type. What used to
// sit inline in each adapter -- encode, build, send, close, check the status,
// decode -- lives here once.
func sendJSON[T any](ctx context.Context, t transport, method, endpoint string, body any, labels writeLabels) (T, error) {
	var decoded T

	resp, err := t.doJSON(ctx, method, endpoint, body, labels)
	if err != nil {
		return decoded, err
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return decoded, t.responseError(resp, labels.errorLabels)
	}
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return decoded, credentialSafeUserError(labels.decodeAction, err, t.token())
	}
	return decoded, nil
}

// send performs one authenticated Helix write whose success carries no body to
// read: Twitch answers Modify Channel Information with 204 No Content.
func send(ctx context.Context, t transport, method, endpoint string, body any, labels writeLabels) error {
	resp, err := t.doJSON(ctx, method, endpoint, body, labels)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !isSuccess(resp) {
		return t.responseError(resp, labels.errorLabels)
	}
	return nil
}

// doJSON builds one JSON Helix request and sends it. The caller owns the
// returned response and is responsible for closing its body.
func (t transport) doJSON(ctx context.Context, method, endpoint string, body any, labels writeLabels) (*http.Response, error) {
	req, err := t.newJSONRequest(ctx, method, endpoint, body, labels)
	if err != nil {
		return nil, err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, credentialSafeUserError(labels.action, err, t.token())
	}
	return resp, nil
}

// clampLimit forces a caller's requested page size into the range Twitch
// accepts for one endpoint.
//
// A limit of zero or less means the caller expressed no preference and gets
// fallback. Anything above maximum is lowered rather than passed on, because
// Twitch rejects an over-large page outright. Every paged adapter applied
// these two rules itself with its own pair of constants.
func clampLimit(limit, fallback, maximum int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > maximum {
		return maximum
	}
	return limit
}

// queryURL returns endpoint carrying values as its query string, replacing any
// parameter of the same name the endpoint already had.
//
// The only error it can return is a malformed endpoint, which in practice
// means a test or config pointed an adapter at something url.Parse rejects.
// Callers report that under their own "create Twitch ... request" message, so
// the error is handed back unwrapped.
func queryURL(endpoint string, values url.Values) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for name, list := range values {
		query.Del(name)
		for _, value := range list {
			query.Add(name, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

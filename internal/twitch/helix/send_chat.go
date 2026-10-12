package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/jsonbody"
)

var errInvalidSendResponse = errors.New("invalid Twitch send acknowledgement")

// SendChatRequest contains the documented user-token sending fields.
type SendChatRequest struct {
	// ForSourceOnly is omitted for user tokens and explicitly selected for app tokens.
	ForSourceOnly    *bool  `json:"for_source_only,omitempty"`
	BroadcasterID    string `json:"broadcaster_id"`
	SenderID         string `json:"sender_id"`
	Message          string `json:"message"`
	ReplyToMessageID string `json:"reply_parent_message_id,omitempty"`
}

type DropReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type SendChatResult struct {
	MessageID  string      `json:"message_id"`
	IsSent     bool        `json:"is_sent"`
	DropReason *DropReason `json:"drop_reason"`
}

// SendChatClient sends once. Ambiguous responses are never retried.
type SendChatClient struct {
	transport
	endpoint string
	timeout  time.Duration
}

func NewSendChatClient(cfg ClientConfig) *SendChatClient {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &SendChatClient{transport: newTransport(cfg.HTTPClient, cfg.ClientID, cfg.OAuthTokenSource, cfg.OAuthToken, cfg.RetryPolicy), endpoint: endpointOrDefault(cfg.Endpoint, "https://api.twitch.tv/helix/chat/messages"), timeout: timeout}
}
func (c *SendChatClient) Send(ctx context.Context, input SendChatRequest) (SendChatResult, int, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return SendChatResult{}, 0, err
		}
		bounded, cancel := context.WithTimeout(ctx, c.timeout)
		result, status, err := c.sendOnce(bounded, input)
		cancel()
		var failure *HTTPError
		if err == nil || !errors.As(err, &failure) || !failure.RetryableSend() || !c.retry.Wait || attempt >= c.retry.MaxRetries {
			return result, status, err
		}
		delay := rateDelay(failure, c.retry.Now())
		if delay > c.retry.MaxDelay {
			return result, status, err
		}
		if waitErr := c.retry.WaitFunc(ctx, delay); waitErr != nil {
			return result, status, fmt.Errorf("wait for Twitch rate limit: %w", waitErr)
		}
	}
}
func (c *SendChatClient) sendOnce(ctx context.Context, input SendChatRequest) (SendChatResult, int, error) {
	req, err := c.newJSONRequest(ctx, http.MethodPost, c.endpoint, input, writeLabels{encodeAction: "encode chat send", createAction: "create chat send"})
	if err != nil {
		return SendChatResult{}, 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SendChatResult{}, 0, &SendError{Cause: credentialSafeUserError("send chat", err, c.token()), Ambiguous: true}
	}
	defer resp.Body.Close()
	if !isSuccess(resp) {
		return SendChatResult{}, resp.StatusCode, c.responseError(resp, errorLabels{action: "send chat", readAction: "read send response", endpoint: "Send Chat Message"})
	}
	var payload struct {
		Data []SendChatResult `json:"data"`
	}
	if err = jsonbody.Decode(resp.Body, maxResponseBodySize, &payload); err != nil {
		return SendChatResult{}, resp.StatusCode, &SendError{Cause: credentialSafeUserError("decode send response", err, c.token()), Ambiguous: true}
	}
	if len(payload.Data) != 1 {
		return SendChatResult{}, resp.StatusCode, &SendError{Cause: errInvalidSendResponse, Ambiguous: true}
	}
	if payload.Data[0].IsSent && payload.Data[0].MessageID == "" {
		return SendChatResult{}, resp.StatusCode, &SendError{Cause: errInvalidSendResponse, Ambiguous: true}
	}
	if reason := payload.Data[0].DropReason; reason != nil {
		credentials := twitch.TokenCredentials{OAuthToken: c.token()}
		reason.Code = redactCredentials(reason.Code, credentials)
		reason.Message = redactCredentials(reason.Message, credentials)
	}
	return payload.Data[0], resp.StatusCode, nil
}

// SendError describes an acknowledged-send failure without weakening ambiguity semantics.
type SendError struct {
	Cause     error
	Ambiguous bool
}

func (e *SendError) Error() string { return e.Cause.Error() }
func (e *SendError) Unwrap() error { return e.Cause }

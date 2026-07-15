package app

import (
	"context"
	"errors"
	"sync"

	"github.com/worxbend/twi/internal/twitch"
)

var (
	ErrFakeChatClientClosed     = errors.New("fake chat client closed")
	ErrFakeChatClientBufferFull = errors.New("fake chat client buffer full")
)

type FakeChatClient struct {
	messages chan twitch.ChatMessage
	states   chan ConnectionState

	mu               sync.Mutex
	closed           bool
	sends            []SendRequest
	results          []fakeSendResult
	reconnects       int
	reconnectResults []error
}

var _ ChatClient = (*FakeChatClient)(nil)

type fakeSendResult struct {
	result SendResult
	err    error
}

func NewFakeChatClient(buffer int) *FakeChatClient {
	if buffer < 0 {
		buffer = 0
	}
	return &FakeChatClient{
		messages: make(chan twitch.ChatMessage, buffer),
		states:   make(chan ConnectionState, buffer),
	}
}

func (c *FakeChatClient) Messages() <-chan twitch.ChatMessage {
	return c.messages
}

func (c *FakeChatClient) ConnectionStates() <-chan ConnectionState {
	return c.states
}

func (c *FakeChatClient) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return SendResult{}, ErrFakeChatClientClosed
	}

	c.sends = append(c.sends, req)
	if len(c.results) == 0 {
		return SendResult{}, nil
	}

	next := c.results[0]
	c.results = c.results[1:]
	return next.result, next.err
}

func (c *FakeChatClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	close(c.messages)
	close(c.states)
	return nil
}

func (c *FakeChatClient) Reconnect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrFakeChatClientClosed
	}

	c.reconnects++
	if len(c.reconnectResults) == 0 {
		return nil
	}
	err := c.reconnectResults[0]
	c.reconnectResults = c.reconnectResults[1:]
	return err
}

func (c *FakeChatClient) FeedMessage(msg twitch.ChatMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrFakeChatClientClosed
	}

	select {
	case c.messages <- msg:
		return nil
	default:
		return ErrFakeChatClientBufferFull
	}
}

func (c *FakeChatClient) FeedConnectionState(state ConnectionState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrFakeChatClientClosed
	}

	select {
	case c.states <- state:
		return nil
	default:
		return ErrFakeChatClientBufferFull
	}
}

func (c *FakeChatClient) QueueSendResult(result SendResult, err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrFakeChatClientClosed
	}
	c.results = append(c.results, fakeSendResult{result: result, err: err})
	return nil
}

func (c *FakeChatClient) QueueReconnectError(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrFakeChatClientClosed
	}
	c.reconnectResults = append(c.reconnectResults, err)
	return nil
}

func (c *FakeChatClient) SentRequests() []SendRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	sends := make([]SendRequest, len(c.sends))
	copy(sends, c.sends)
	return sends
}

func (c *FakeChatClient) ReconnectCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reconnects
}

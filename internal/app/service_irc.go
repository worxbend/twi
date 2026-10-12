package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/twitch"
)

// ServiceIRCClient supplies IRC's optional enqueue/join capabilities around the
// same live lifecycle used by EventSub and headless capture. It owns no network
// session and never claims an IRC enqueue is an acknowledged Twitch send.
type ServiceIRCClient struct {
	*ServiceChatSource
	mu             sync.RWMutex
	transport      twitch.ChatClient
	joined, parted map[string]bool
	parent         context.Context
}

func NewServiceIRCClient(ctx context.Context, factory LiveChatTransportFactory, channels []string, recorder ChatRecorder) (*ServiceIRCClient, error) {
	client := &ServiceIRCClient{joined: map[string]bool{}, parted: map[string]bool{}, parent: ctx}
	source, err := NewServiceChatSource(ctx, func(context.Context) (*chatservice.Service, error) {
		return chatservice.New(chatservice.Config{Channels: channels, AllowEmptySelection: true, Factory: func(ctx context.Context) (chatservice.Reader, error) { return factory(ctx) }, ReaderChanged: client.readerChanged})
	}, "irc", recorder)
	if err != nil {
		return nil, err
	}
	client.ServiceChatSource = source
	return client, nil
}
func (c *ServiceIRCClient) readerChanged(reader chatservice.Reader) {
	c.mu.Lock()
	transport, _ := reader.(twitch.ChatClient)
	c.transport = transport
	joined, parted := []string{}, []string{}
	for channel := range c.joined {
		joined = append(joined, channel)
	}
	for channel := range c.parted {
		parted = append(parted, channel)
	}
	c.mu.Unlock()
	if joiner, ok := transport.(twitch.ChannelJoiner); ok {
		ctx, cancel := context.WithTimeout(c.parent, 30*time.Second)
		defer cancel()
		for _, channel := range joined {
			_ = joiner.Join(ctx, channel)
		}
		for _, channel := range parted {
			_ = joiner.Depart(ctx, channel)
		}
	}
}
func (c *ServiceIRCClient) current() (twitch.ChatClient, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.transport == nil {
		return nil, ErrLiveChatDisconnected
	}
	return c.transport, nil
}
func (c *ServiceIRCClient) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	transport, err := c.current()
	if err != nil {
		return SendResult{}, err
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return SendResult{}, errors.New("message text cannot be empty")
	}
	err = transport.Send(ctx, twitch.OutboundMessage{Channel: request.Channel, Text: text, ReplyToMessageID: request.ReplyToMessageID, Action: request.Action})
	if err != nil {
		return SendResult{RateLimited: isRateLimited(err), Detail: credentialSafeSendDetail(err)}, credentialSafeSendError(err)
	}
	return SendResult{QueuedAt: time.Now(), Detail: "queued (delivery unconfirmed)"}, nil
}
func (c *ServiceIRCClient) JoinChannel(ctx context.Context, value string) error {
	channel, err := twitch.ParseChannel(value)
	if err != nil {
		return err
	}
	transport, err := c.current()
	if err != nil {
		return err
	}
	joiner, ok := transport.(twitch.ChannelJoiner)
	if !ok {
		return errors.New("channel join unavailable")
	}
	if err := joiner.Join(ctx, channel.String()); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.parted, channel.String())
	c.joined[channel.String()] = true
	c.mu.Unlock()
	c.setCaptureSelected(ctx, channel.String(), true)
	return nil
}
func (c *ServiceIRCClient) PartChannel(ctx context.Context, value string) error {
	channel, err := twitch.ParseChannel(value)
	if err != nil {
		return err
	}
	transport, err := c.current()
	if err != nil {
		return err
	}
	joiner, ok := transport.(twitch.ChannelJoiner)
	if !ok {
		return errors.New("channel part unavailable")
	}
	if err := joiner.Depart(ctx, channel.String()); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.joined, channel.String())
	c.parted[channel.String()] = true
	c.mu.Unlock()
	c.setCaptureSelected(ctx, channel.String(), false)
	return nil
}

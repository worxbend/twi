package twitch

import (
	"errors"
	"strings"
)

// NormalizeChannel turns a channel name in any of the forms a user, a config
// file or a command-line flag might supply it into the plain name twi uses
// internally: no leading "#", no surrounding whitespace.
//
// The trims must run outermost-first -- whitespace, then the optional "#".
// The other order silently fails on a value with leading whitespace, because
// TrimPrefix sees the space rather than the "#" and leaves the "#" in place.
// A config line written the natural way, `channels = alpha, #beta`, produces
// exactly that value: the split leaves " #beta" with its leading space.
//
// Capitalisation is preserved, because this is the form shown to the user and
// people write their own channel with the capitals they chose. Use ChannelKey
// when comparing two names or using one as a map key.
func NormalizeChannel(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "#")
}

// ChannelKey is NormalizeChannel plus lower-casing: the form used to decide
// whether two channel names mean the same channel, and the form Twitch IRC
// expects on the wire.
//
// The lower-casing is a protocol requirement, not a formatting choice: IRC
// channel names are case-insensitive, so "#Foo" and "#foo" are one channel.
// Do not use this for anything the user sees -- that should keep their capitals.
func ChannelKey(value string) string {
	return strings.ToLower(NormalizeChannel(value))
}

// NormalizeChannels applies NormalizeChannel to every value and drops the ones
// that normalize to nothing, so a trailing comma or a blank list entry does not
// become an empty channel name.
func NormalizeChannels(values []string) []string {
	channels := make([]string, 0, len(values))
	for _, value := range values {
		if channel := NormalizeChannel(value); channel != "" {
			channels = append(channels, channel)
		}
	}
	return channels
}

// Channel is a validated canonical Twitch login. Its zero value is invalid.
// ParseChannel accepts channel selectors; mentions are parsed separately.
type Channel struct{ login string }

func ParseChannel(value string) (Channel, error) {
	login := ChannelKey(value)
	if len(login) < 1 || len(login) > 25 {
		return Channel{}, errors.New("invalid channel login")
	}
	for _, r := range login {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return Channel{}, errors.New("invalid channel login")
		}
	}
	return Channel{login: login}, nil
}
func (c Channel) String() string { return c.login }
func (c Channel) Valid() bool    { return c.login != "" }

// ParseChannels validates every selector and removes duplicates in input order.
// Unlike NormalizeChannels, empty selectors are errors.
func ParseChannels(values []string) ([]Channel, error) {
	result := make([]Channel, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		channel, err := ParseChannel(value)
		if err != nil {
			return nil, err
		}
		if !seen[channel.login] {
			seen[channel.login] = true
			result = append(result, channel)
		}
	}
	return result, nil
}

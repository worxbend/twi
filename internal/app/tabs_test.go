package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/config"
)

func TestTabBarShowsConfiguredUsernameAndActiveChat(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "viewer"
	cfg.DefaultChannels = []string{"alpha", "beta"}
	model := newMockModel("alpha", cfg)

	line := model.tabBarLine(88)
	if !strings.Contains(line, "@viewer  #alpha") {
		t.Fatalf("tab bar missing username and active chat: %q", line)
	}

	model.channels.setActive("beta")
	line = model.tabBarLine(88)
	if !strings.Contains(line, "@viewer  #beta") {
		t.Fatalf("tab bar did not follow active chat: %q", line)
	}
}

func TestTabBarShowsChatWithoutConfiguredUsername(t *testing.T) {
	model := newMockModel("example", config.Default())

	line := model.tabBarLine(88)
	if !strings.Contains(line, "#example") {
		t.Fatalf("tab bar missing active chat: %q", line)
	}
	if strings.Contains(line, "@") {
		t.Fatalf("tab bar rendered an empty username: %q", line)
	}
}

func TestTabBarKeepsExactWidthAndUnicodeContextAtNarrowWidth(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "視聴者"
	model := newMockModel("配信", cfg)

	const width = 36
	line := model.tabBarLine(width)
	if got := lipgloss.Width(line); got != width {
		t.Fatalf("tab bar width = %d, want %d: %q", got, width, line)
	}
	if !strings.Contains(line, "*1:Chat") {
		if !strings.Contains(line, "*1") {
			t.Fatalf("narrow tab bar lost active tab control: %q", line)
		}
	}
	if !strings.Contains(line, "@視聴者  #配信") {
		t.Fatalf("narrow tab bar lost Unicode identity context: %q", line)
	}
}

func TestTabBarPrioritizesActiveChatAtVeryNarrowWidths(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "long_viewer_name"
	model := newMockModel("example", cfg)

	line := model.tabBarLine(15)
	if !strings.Contains(line, "*1") || !strings.Contains(line, "#example") {
		t.Fatalf("very narrow tab bar missing active tab or chat: %q", line)
	}
	if strings.Contains(line, "long_viewer_name") {
		t.Fatalf("very narrow tab bar kept username ahead of active chat: %q", line)
	}
}

func TestTabBarContextSanitizesTerminalControlCharacters(t *testing.T) {
	cfg := config.Default()
	cfg.Twitch.Username = "view\x1b[31mer\nname"
	model := newMockModel("room\r\nname", cfg)

	username, channel := model.tabBarContextParts()
	context := username + channel
	for _, control := range []string{"\x1b", "\n", "\r"} {
		if strings.Contains(context, control) {
			t.Fatalf("tab bar context retained control %q: %q", control, context)
		}
	}
	if !strings.Contains(username, "�") || !strings.Contains(channel, "�") {
		t.Fatalf("tab bar context did not visibly replace controls: %q %q", username, channel)
	}
}

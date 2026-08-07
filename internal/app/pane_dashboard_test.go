package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
)

func TestDashboardPanesExposeIconTitlesAndComposerIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	cfg.DefaultChannels = []string{"alpha", "beta", "gamma"}
	model := newMockModel("alpha", cfg)
	model.width, model.height = 140, 24
	model.appendActivity(activityEntry{Kind: activityFollow, Text: "NewViewer followed"})

	plain := ansi.Strip(model.View())
	for _, want := range []string{
		"💬 Chat · #alpha",
		"📡 Channels",
		"⚡ Activity",
		"✉ Chat · #alpha · ready",
		"⌨ ctrl+p",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, plain)
		}
	}
}

func TestModalAndTabPanesExposeIconTitles(t *testing.T) {
	model := newMockModel("alpha", config.Default())
	model.width, model.height = 100, 24
	layout := shellLayout{
		width:                       100,
		paletteHeight:               8,
		paletteContentHeight:        6,
		paletteFramed:               true,
		inspectHeight:               8,
		inspectContentHeight:        6,
		inspectFramed:               true,
		emotePickerHeight:           8,
		emotePickerContentHeight:    6,
		emotePickerFramed:           true,
		categoryPickerHeight:        8,
		categoryPickerContentHeight: 6,
		categoryPickerFramed:        true,
		streamInfoHeight:            8,
		streamInfoContentHeight:     6,
		streamInfoFramed:            true,
		miscHeight:                  8,
		miscContentHeight:           6,
		miscFramed:                  true,
	}

	views := []struct {
		name string
		want string
		view string
	}{
		{name: "command palette", want: "⌘ Command Palette", view: model.commandPaletteView(layout)},
		{name: "inspect", want: "🔎 Inspect", view: model.inspectView(layout)},
		{name: "emote picker", want: "😀 Emote Search", view: model.emotePickerView(layout)},
		{name: "category picker", want: "🎮 Category Search", view: model.categoryPickerView(layout)},
		{name: "stream info", want: "📺 Stream Info", view: model.streamInfoView(layout)},
		{name: "misc", want: "⏺ Stream Markers", view: model.miscView(layout)},
	}
	for _, test := range views {
		if plain := ansi.Strip(test.view); !strings.Contains(plain, test.want) {
			t.Errorf("%s missing title %q:\n%s", test.name, test.want, plain)
		}
	}
}

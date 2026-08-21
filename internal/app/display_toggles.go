package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
)

// Display toggles let the chat surface be reshaped without leaving the app.
// Each one applies immediately and is written back to the effective config
// file, so a preference found by experimenting survives the next launch -
// the same round-trip the theme picker uses on enter.
//
// Persisting is best-effort: a failed write leaves the runtime change in
// place and reports the reason in the status bar rather than reverting a
// change the user just asked for.

// cycleMessageLayout advances the chat layout: inline → grouped → compact.
func (m *shellModel) cycleMessageLayout() {
	switch m.display.messageLayout {
	case render.LayoutInline:
		m.display.messageLayout = render.LayoutGrouped
	case render.LayoutGrouped:
		m.display.messageLayout = render.LayoutCompact
	default:
		m.display.messageLayout = render.LayoutInline
	}
	m.persistDisplayPreference("layout "+string(m.display.messageLayout), func(cfg *config.Config) {
		cfg.Features.MessageLayout = string(m.display.messageLayout)
	})
}

// cycleBadgeMode advances badge rendering: glyph → text → off.
func (m *shellModel) cycleBadgeMode() {
	switch m.display.badgeMode {
	case render.BadgeModeGlyph:
		m.display.badgeMode = render.BadgeModeText
	case render.BadgeModeText:
		m.display.badgeMode = render.BadgeModeOff
	default:
		m.display.badgeMode = render.BadgeModeGlyph
	}
	m.persistDisplayPreference("badges "+string(m.display.badgeMode), func(cfg *config.Config) {
		cfg.Features.BadgeMode = string(m.display.badgeMode)
	})
}

// toggleEmoteHighlight turns the emote/emoji chip background on and off.
func (m *shellModel) toggleEmoteHighlight() {
	m.display.highlightEmotes = !m.display.highlightEmotes
	m.persistDisplayPreference(onOffLabel("emote highlight", m.display.highlightEmotes), func(cfg *config.Config) {
		cfg.Features.HighlightEmotes = m.display.highlightEmotes
	})
}

// toggleFullUsername turns the "DisplayName (login)" form on and off.
func (m *shellModel) toggleFullUsername() {
	m.display.fullUsername = !m.display.fullUsername
	m.persistDisplayPreference(onOffLabel("full usernames", m.display.fullUsername), func(cfg *config.Config) {
		cfg.Features.FullUsername = m.display.fullUsername
	})
}

func onOffLabel(name string, on bool) string {
	if on {
		return name + " on"
	}
	return name + " off"
}

// persistDisplayPreference applies mutate to the effective config, saves it,
// and reports the outcome through the composer's status feedback.
func (m *shellModel) persistDisplayPreference(label string, mutate func(*config.Config)) {
	cfg := m.effectiveConfig
	mutate(&cfg)
	m.effectiveConfig = cfg

	state := m.activeChannelState()
	if state == nil {
		return
	}
	if err := config.WriteNonSecretFile(cfg.Path, cfg); err != nil {
		state.sendFeedback = label + " (not saved: " + config.RedactDisplayValue(err.Error()) + ")"
		return
	}
	state.sendFeedback = label
}

// handleDisplayToggleKey routes the display-toggle hotkeys. It returns false
// when msg is not one of them so the caller can keep dispatching.
//
// The chosen keys avoid the control codes terminals reserve: ctrl+h is
// backspace, ctrl+i is tab, and ctrl+m is enter, so none of those can carry
// a binding here.
func (m *shellModel) handleDisplayToggleKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyCtrlG:
		m.cycleMessageLayout()
	case tea.KeyCtrlB:
		m.cycleBadgeMode()
	case tea.KeyCtrlY:
		m.toggleEmoteHighlight()
	case tea.KeyCtrlN:
		m.toggleFullUsername()
	default:
		return false
	}
	m.clampScroll()
	return true
}

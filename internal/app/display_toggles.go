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
	switch m.messageLayout {
	case render.LayoutInline:
		m.messageLayout = render.LayoutGrouped
	case render.LayoutGrouped:
		m.messageLayout = render.LayoutCompact
	default:
		m.messageLayout = render.LayoutInline
	}
	m.persistDisplayPreference("layout "+string(m.messageLayout), func(cfg *config.Config) {
		cfg.Features.MessageLayout = string(m.messageLayout)
	})
}

// cycleBadgeMode advances badge rendering: glyph → text → off.
func (m *shellModel) cycleBadgeMode() {
	switch m.badgeMode {
	case render.BadgeModeGlyph:
		m.badgeMode = render.BadgeModeText
	case render.BadgeModeText:
		m.badgeMode = render.BadgeModeOff
	default:
		m.badgeMode = render.BadgeModeGlyph
	}
	m.persistDisplayPreference("badges "+string(m.badgeMode), func(cfg *config.Config) {
		cfg.Features.BadgeMode = string(m.badgeMode)
	})
}

// toggleEmoteHighlight turns the emote/emoji chip background on and off.
func (m *shellModel) toggleEmoteHighlight() {
	m.highlightEmotes = !m.highlightEmotes
	m.persistDisplayPreference(onOffLabel("emote highlight", m.highlightEmotes), func(cfg *config.Config) {
		cfg.Features.HighlightEmotes = m.highlightEmotes
	})
}

// toggleFullUsername turns the "DisplayName (login)" form on and off.
func (m *shellModel) toggleFullUsername() {
	m.fullUsername = !m.fullUsername
	m.persistDisplayPreference(onOffLabel("full usernames", m.fullUsername), func(cfg *config.Config) {
		cfg.Features.FullUsername = m.fullUsername
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

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/worxbend/twi/internal/twitch"
)

const streamInfoRequestTimeout = 5 * time.Second

// streamInfoField is one editable row on the Stream Info tab.
type streamInfoField int

const (
	streamInfoFieldTitle streamInfoField = iota
	streamInfoFieldCategory
	streamInfoFieldLanguage
	streamInfoFieldTags
	streamInfoFieldCount
)

var streamInfoFieldLabels = [streamInfoFieldCount]string{
	streamInfoFieldTitle:    "Title",
	streamInfoFieldCategory: "Category",
	streamInfoFieldLanguage: "Language",
	streamInfoFieldTags:     "Tags",
}

// streamInfoState drives the Stream Info tab: the fields hold the
// user-editable working values (seeded from the last successful load or
// save), while original holds the last confirmed Twitch-side snapshot so
// saving only sends fields that actually changed.
type streamInfoState struct {
	loading       bool
	loaded        bool
	loadErr       string
	broadcasterID string
	original      twitch.ChannelInfo

	title    string
	category string
	language string
	tags     string // comma-separated for display/edit

	selected   streamInfoField
	editing    bool
	editBuffer string

	saving  bool
	saveErr string
	saveOK  bool
}

type streamInfoLoadedMsg struct {
	broadcasterID string
	info          twitch.ChannelInfo
	err           error
}

type streamInfoSavedMsg struct {
	info twitch.ChannelInfo
	err  error
}

// scheduleStreamInfoLoad fetches the logged-in broadcaster's current channel
// info the first time the Stream Info tab opens (or after a failed load).
// Repeat opens reuse the already-resolved broadcaster ID instead of looking
// it up again.
func (m *mockShellModel) scheduleStreamInfoLoad() tea.Cmd {
	if m.channelManager == nil {
		m.streamInfo.loadErr = "Stream Info requires Twitch API credentials (client ID + OAuth token); run `twi login`."
		return nil
	}
	if m.streamInfo.loading {
		return nil
	}
	m.streamInfo.loading = true
	m.streamInfo.loadErr = ""

	channelManager := m.channelManager
	userLookup := m.selfUserLookup
	username := strings.TrimSpace(m.effectiveConfig.Twitch.Username)
	knownID := m.streamInfo.broadcasterID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), streamInfoRequestTimeout)
		defer cancel()

		id := knownID
		if id == "" {
			if userLookup == nil || username == "" {
				return streamInfoLoadedMsg{err: fmt.Errorf("resolve your Twitch user ID: missing username or user lookup")}
			}
			users, err := userLookup.GetUsers(ctx, twitch.UserLookupRequest{UserLogins: []string{username}})
			if err != nil {
				return streamInfoLoadedMsg{err: err}
			}
			for _, u := range users {
				if strings.EqualFold(u.Login, username) {
					id = u.UserID
					break
				}
			}
			if id == "" {
				return streamInfoLoadedMsg{err: fmt.Errorf("could not resolve a Twitch user ID for %q", username)}
			}
		}

		info, err := channelManager.GetChannelInformation(ctx, id)
		if err != nil {
			return streamInfoLoadedMsg{broadcasterID: id, err: err}
		}
		return streamInfoLoadedMsg{broadcasterID: id, info: info}
	}
}

func (m mockShellModel) applyStreamInfoLoaded(msg streamInfoLoadedMsg) mockShellModel {
	m.streamInfo.loading = false
	if msg.broadcasterID != "" {
		m.streamInfo.broadcasterID = msg.broadcasterID
	}
	if msg.err != nil {
		m.streamInfo.loadErr = msg.err.Error()
		return m
	}
	m.streamInfo.loadErr = ""
	m.streamInfo.loaded = true
	m.streamInfo.original = msg.info
	m.streamInfo.title = msg.info.Title
	m.streamInfo.category = msg.info.GameName
	m.streamInfo.language = msg.info.Language
	m.streamInfo.tags = strings.Join(msg.info.Tags, ", ")
	return m
}

// scheduleStreamInfoSave diffs the working field values against the last
// confirmed snapshot and sends only the changed fields to Twitch. Changing
// the category requires resolving its Twitch game ID first (Twitch's Modify
// Channel Information endpoint takes game_id, not a display name).
func (m *mockShellModel) scheduleStreamInfoSave() tea.Cmd {
	if m.channelManager == nil || m.streamInfo.broadcasterID == "" || m.streamInfo.saving {
		return nil
	}

	original := m.streamInfo.original
	title := strings.TrimSpace(m.streamInfo.title)
	category := strings.TrimSpace(m.streamInfo.category)
	language := strings.TrimSpace(m.streamInfo.language)
	tags := parseStreamInfoTags(m.streamInfo.tags)
	broadcasterID := m.streamInfo.broadcasterID
	channelManager := m.channelManager
	gameLookup := m.gameLookup

	m.streamInfo.saving = true
	m.streamInfo.saveErr = ""
	m.streamInfo.saveOK = false

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), streamInfoRequestTimeout)
		defer cancel()

		update := twitch.ChannelInfoUpdate{}
		next := original
		if title != original.Title {
			update.Title = &title
			next.Title = title
		}
		if language != original.Language {
			update.Language = &language
			next.Language = language
		}
		if !slices.Equal(tags, original.Tags) {
			t := tags
			update.Tags = &t
			next.Tags = tags
		}
		if category != original.GameName {
			switch {
			case category == "":
				empty := ""
				update.GameID = &empty
				next.GameID = ""
				next.GameName = ""
			case gameLookup == nil:
				return streamInfoSavedMsg{err: fmt.Errorf("change category: no Twitch category lookup configured")}
			default:
				game, ok, err := gameLookup.GetGameByName(ctx, category)
				if err != nil {
					return streamInfoSavedMsg{err: fmt.Errorf("look up category %q: %w", category, err)}
				}
				if !ok {
					return streamInfoSavedMsg{err: fmt.Errorf("no Twitch category named %q", category)}
				}
				update.GameID = &game.ID
				next.GameID = game.ID
				next.GameName = game.Name
			}
		}

		if update.IsEmpty() {
			return streamInfoSavedMsg{info: next}
		}
		if err := channelManager.ModifyChannelInformation(ctx, broadcasterID, update); err != nil {
			return streamInfoSavedMsg{err: err}
		}
		return streamInfoSavedMsg{info: next}
	}
}

func (m mockShellModel) applyStreamInfoSaved(msg streamInfoSavedMsg) mockShellModel {
	m.streamInfo.saving = false
	if msg.err != nil {
		m.streamInfo.saveErr = msg.err.Error()
		m.streamInfo.saveOK = false
		return m
	}
	m.streamInfo.saveErr = ""
	m.streamInfo.saveOK = true
	m.streamInfo.original = msg.info
	m.streamInfo.title = msg.info.Title
	m.streamInfo.category = msg.info.GameName
	m.streamInfo.language = msg.info.Language
	m.streamInfo.tags = strings.Join(msg.info.Tags, ", ")
	return m
}

func parseStreamInfoTags(raw string) []string {
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			tags = append(tags, part)
		}
	}
	return tags
}

// handleStreamInfoKey handles all keys while the Stream Info tab is active
// and no overlay (palette/inspect/theme/emotes) is open; see the KeyMsg
// dispatch order in Update.
func (m mockShellModel) handleStreamInfoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.streamInfo.editing {
		switch msg.Type {
		case tea.KeyEsc:
			m.streamInfo.editing = false
			m.streamInfo.editBuffer = ""
		case tea.KeyEnter:
			m.commitStreamInfoEdit()
		case tea.KeyBackspace:
			if n := len(m.streamInfo.editBuffer); n > 0 {
				_, size := utf8.DecodeLastRuneInString(m.streamInfo.editBuffer)
				m.streamInfo.editBuffer = m.streamInfo.editBuffer[:n-size]
			}
		case tea.KeySpace:
			m.streamInfo.editBuffer += " "
		case tea.KeyRunes:
			m.streamInfo.editBuffer += string(msg.Runes)
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyUp:
		m.moveStreamInfoSelection(-1)
	case tea.KeyDown, tea.KeyTab:
		m.moveStreamInfoSelection(1)
	case tea.KeyEnter:
		m.startStreamInfoEdit()
	case tea.KeyCtrlS:
		if cmd := m.scheduleStreamInfoSave(); cmd != nil {
			return m, cmd
		}
	case tea.KeyEsc:
		m.streamInfo.saveErr = ""
		m.streamInfo.saveOK = false
	}
	return m, nil
}

func (m *mockShellModel) moveStreamInfoSelection(delta int) {
	selected := (int(m.streamInfo.selected) + delta + int(streamInfoFieldCount)) % int(streamInfoFieldCount)
	m.streamInfo.selected = streamInfoField(selected)
}

func (m *mockShellModel) startStreamInfoEdit() {
	if !m.streamInfo.loaded || m.streamInfo.loading {
		return
	}
	m.streamInfo.editing = true
	m.streamInfo.editBuffer = m.streamInfoFieldValue(m.streamInfo.selected)
}

func (m *mockShellModel) commitStreamInfoEdit() {
	m.setStreamInfoFieldValue(m.streamInfo.selected, m.streamInfo.editBuffer)
	m.streamInfo.editing = false
	m.streamInfo.editBuffer = ""
}

func (m mockShellModel) streamInfoFieldValue(field streamInfoField) string {
	switch field {
	case streamInfoFieldTitle:
		return m.streamInfo.title
	case streamInfoFieldCategory:
		return m.streamInfo.category
	case streamInfoFieldLanguage:
		return m.streamInfo.language
	case streamInfoFieldTags:
		return m.streamInfo.tags
	default:
		return ""
	}
}

func (m *mockShellModel) setStreamInfoFieldValue(field streamInfoField, value string) {
	switch field {
	case streamInfoFieldTitle:
		m.streamInfo.title = value
	case streamInfoFieldCategory:
		m.streamInfo.category = value
	case streamInfoFieldLanguage:
		m.streamInfo.language = value
	case streamInfoFieldTags:
		m.streamInfo.tags = value
	}
}

func (m mockShellModel) streamInfoView(layout mockShellLayout) string {
	contentWidth := layout.width
	if layout.streamInfoFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.streamInfoLines(contentWidth, layout.streamInfoContentHeight)
	content := strings.Join(lines, "\n")
	if !layout.streamInfoFramed {
		return fitBlock(content, layout.width, layout.streamInfoHeight)
	}
	return lipgloss.NewStyle().
		Width(clampMin(layout.width-2, 0)).
		Height(layout.streamInfoContentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(m.theme.Accent)).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Background)).
		Padding(0, 1).
		Render(content)
}

func (m mockShellModel) streamInfoLines(width, height int) []string {
	if height <= 0 {
		return nil
	}

	var lines []string
	switch {
	case m.channelManager == nil:
		lines = []string{
			" Stream Info",
			" Unavailable: requires Twitch API credentials (client ID + OAuth token).",
			" Run `twi login` to grant channel:manage:broadcast, then restart twi.",
		}
	case m.streamInfo.loading && !m.streamInfo.loaded:
		lines = []string{" Stream Info", " Loading current stream info..."}
	case m.streamInfo.loadErr != "":
		lines = []string{
			" Stream Info",
			" Load failed: " + m.streamInfo.loadErr,
			" Reopen the tab (alt+2) to retry.",
		}
	default:
		lines = m.streamInfoFieldLines()
	}

	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, fitLine(line, width))
	}
	return out
}

func (m mockShellModel) streamInfoFieldLines() []string {
	header := " Stream Info (enter=edit, ctrl+s=save, esc=dismiss)"
	switch {
	case m.streamInfo.saving:
		header = " Stream Info (saving...)"
	case m.streamInfo.saveErr != "":
		header = " Stream Info: save failed: " + m.streamInfo.saveErr
	case m.streamInfo.saveOK:
		header = " Stream Info: saved"
	}
	lines := []string{header}

	for field := streamInfoField(0); field < streamInfoFieldCount; field++ {
		prefix := "  "
		value := m.streamInfoFieldValue(field)
		editingThis := m.streamInfo.editing && m.streamInfo.selected == field
		if editingThis {
			prefix = "> "
			value = m.streamInfo.editBuffer + "█"
		} else if m.streamInfo.selected == field {
			prefix = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%s: %s", prefix, streamInfoFieldLabels[field], value))
	}
	return lines
}

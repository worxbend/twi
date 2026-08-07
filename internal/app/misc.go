package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/worxbend/twi/internal/twitch"
)

const miscRequestTimeout = 5 * time.Second

// miscState drives the Misc tab: a list of the broadcaster's own stream
// markers (Twitch Helix "Get Stream Markers", most recent video first) plus
// the ability to create a new one (Twitch Helix "Create Stream Marker",
// which only succeeds while the broadcaster is live).
type miscState struct {
	loading bool
	loaded  bool
	loadErr string
	markers []twitch.StreamMarker

	selected int

	creating   bool
	createErr  string
	createOK   bool
	editing    bool // typing a description for a new marker
	editBuffer string
}

type miscMarkersLoadedMsg struct {
	broadcasterID string
	markers       []twitch.StreamMarker
	err           error
}

type miscMarkerCreatedMsg struct {
	marker twitch.StreamMarker
	err    error
}

// scheduleMiscLoad fetches the logged-in broadcaster's stream markers the
// first time the Misc tab opens (or after a failed load), reusing the
// already-resolved broadcaster ID from shellModel.selfBroadcasterID
// when Stream Info already resolved it.
func (m *shellModel) scheduleMiscLoad() tea.Cmd {
	if m.markerManager == nil {
		m.misc.loadErr = "Misc requires Twitch API credentials (client ID + OAuth token); run `twi login`."
		return nil
	}
	if m.misc.loading {
		return nil
	}
	m.misc.loading = true
	m.misc.loadErr = ""

	markerManager := m.markerManager
	userLookup := m.userLookup
	username := m.effectiveConfig.Twitch.Username
	knownID := m.selfBroadcasterID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), miscRequestTimeout)
		defer cancel()

		id := knownID
		if id == "" {
			resolved, err := resolveSelfBroadcasterID(ctx, userLookup, username)
			if err != nil {
				return miscMarkersLoadedMsg{err: err}
			}
			id = resolved
		}

		markers, err := markerManager.GetStreamMarkers(ctx, id, 0)
		if err != nil {
			return miscMarkersLoadedMsg{broadcasterID: id, err: err}
		}
		return miscMarkersLoadedMsg{broadcasterID: id, markers: markers}
	}
}

func (m shellModel) applyMiscLoaded(msg miscMarkersLoadedMsg) shellModel {
	m.misc.loading = false
	if msg.broadcasterID != "" {
		m.selfBroadcasterID = msg.broadcasterID
	}
	if msg.err != nil {
		m.misc.loadErr = miscErrorMessage(msg.err)
		return m
	}
	m.misc.loadErr = ""
	m.misc.loaded = true
	m.misc.markers = msg.markers
	if m.misc.selected >= len(m.misc.markers) {
		m.misc.selected = 0
	}
	return m
}

// scheduleCreateMarker marks the current moment in the broadcaster's active
// stream with an optional description. Twitch rejects this when the
// broadcaster isn't currently live; that error surfaces as-is in createErr.
func (m *shellModel) scheduleCreateMarker(description string) tea.Cmd {
	if m.markerManager == nil || m.misc.creating {
		return nil
	}
	markerManager := m.markerManager
	userLookup := m.userLookup
	username := m.effectiveConfig.Twitch.Username
	knownID := m.selfBroadcasterID

	m.misc.creating = true
	m.misc.createErr = ""
	m.misc.createOK = false

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), miscRequestTimeout)
		defer cancel()

		id := knownID
		if id == "" {
			resolved, err := resolveSelfBroadcasterID(ctx, userLookup, username)
			if err != nil {
				return miscMarkerCreatedMsg{err: err}
			}
			id = resolved
		}
		marker, err := markerManager.CreateStreamMarker(ctx, id, description)
		if err != nil {
			return miscMarkerCreatedMsg{err: err}
		}
		return miscMarkerCreatedMsg{marker: marker}
	}
}

func (m shellModel) applyMiscMarkerCreated(msg miscMarkerCreatedMsg) shellModel {
	m.misc.creating = false
	if msg.err != nil {
		m.misc.createErr = miscErrorMessage(msg.err)
		m.misc.createOK = false
		return m
	}
	m.misc.createErr = ""
	m.misc.createOK = true
	m.misc.markers = append([]twitch.StreamMarker{msg.marker}, m.misc.markers...)
	m.misc.selected = 0
	return m
}

// miscErrorMessage mirrors streamInfoErrorMessage: a 401 from either stream
// marker endpoint means the current token predates (or otherwise lacks)
// channel:manage:broadcast. A 404 from Get/Create Stream Marker means
// Twitch has no video to attach markers to, which in practice always means
// the channel isn't currently live (Twitch only maintains a markers-eligible
// video while a broadcast is in progress).
func miscErrorMessage(err error) string {
	switch {
	case twitch.IsMissingScope(err):
		return "Your Twitch login is missing the channel:manage:broadcast scope (or the token expired). Run `twi login` to re-authenticate, then reopen this tab (alt+3)."
	case twitch.IsNoVideoFound(err):
		return "Stream markers are not available: you are not currently live. Start streaming, then reopen this tab (alt+3)."
	default:
		return err.Error()
	}
}

func (m shellModel) handleMiscKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.misc.editing {
		switch msg.Type {
		case tea.KeyEsc:
			m.misc.editing = false
			m.misc.editBuffer = ""
		case tea.KeyEnter:
			description := m.misc.editBuffer
			m.misc.editing = false
			m.misc.editBuffer = ""
			return m, m.scheduleCreateMarker(description)
		case tea.KeyBackspace:
			if n := len(m.misc.editBuffer); n > 0 {
				_, size := utf8.DecodeLastRuneInString(m.misc.editBuffer)
				m.misc.editBuffer = m.misc.editBuffer[:n-size]
			}
		case tea.KeySpace:
			m.misc.editBuffer += " "
		case tea.KeyRunes:
			m.misc.editBuffer += string(msg.Runes)
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyUp:
		m.moveMiscSelection(-1)
	case tea.KeyDown, tea.KeyTab:
		m.moveMiscSelection(1)
	case tea.KeyEnter:
		if !m.misc.loaded || m.misc.loading || m.misc.creating {
			return m, nil
		}
		m.misc.editing = true
		m.misc.editBuffer = ""
		m.misc.createErr = ""
		m.misc.createOK = false
	case tea.KeyEsc:
		m.misc.createErr = ""
		m.misc.createOK = false
	}
	return m, nil
}

func (m *shellModel) moveMiscSelection(delta int) {
	if len(m.misc.markers) == 0 {
		m.misc.selected = 0
		return
	}
	m.misc.selected += delta
	if m.misc.selected < 0 {
		m.misc.selected = len(m.misc.markers) - 1
	}
	if m.misc.selected >= len(m.misc.markers) {
		m.misc.selected = 0
	}
}

func (m shellModel) miscView(layout shellLayout) string {
	contentWidth := layout.width
	if layout.miscFramed {
		contentWidth = clampMin(layout.width-4, 1)
	}
	lines := m.miscLines(contentWidth, layout.miscContentHeight)
	content := strings.Join(lines, "\n")
	if !layout.miscFramed {
		return fitBlock(content, layout.width, layout.miscHeight)
	}
	return m.renderPane(paneSpec{
		icon:          "⏺",
		title:         "Stream Markers",
		content:       content,
		width:         layout.width,
		contentHeight: layout.miscContentHeight,
		padding:       1,
		accent:        m.theme.Warning,
		focused:       true,
	})
}

func (m shellModel) miscLines(width, height int) []string {
	if height <= 0 {
		return nil
	}

	var lines []string
	switch {
	case m.markerManager == nil:
		lines = []string{
			" Misc: Stream Markers",
			" Unavailable: requires Twitch API credentials (client ID + OAuth token).",
			" Run `twi login` to grant channel:manage:broadcast, then restart twi.",
		}
	case m.misc.loading && !m.misc.loaded:
		lines = []string{" Misc: Stream Markers", " Loading markers..."}
	case m.misc.loadErr != "":
		lines = append([]string{" Misc: Stream Markers"}, wrapIndentedText("Load failed: "+m.misc.loadErr, width)...)
		lines = append(lines, " Reopen the tab (alt+3) to retry.")
	default:
		lines = m.miscMarkerLines(width)
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

func (m shellModel) miscMarkerLines(width int) []string {
	var header string
	switch {
	case m.misc.editing:
		header = " New marker description (enter=save, esc=cancel): " + m.misc.editBuffer + "█"
	case m.misc.creating:
		header = " Misc: Stream Markers (creating marker...)"
	case m.misc.createErr != "":
		return append([]string{" Misc: Stream Markers"}, wrapIndentedText("Create failed: "+m.misc.createErr, width)...)
	case m.misc.createOK:
		header = " Misc: Stream Markers (marker created!)"
	default:
		header = " Misc: Stream Markers (enter=add marker, up/down=select)"
	}
	lines := []string{header}
	if len(m.misc.markers) == 0 {
		lines = append(lines, "  no markers yet for the current stream/video")
		return lines
	}
	for i, marker := range m.misc.markers {
		prefix := "  "
		if i == m.misc.selected {
			prefix = "> "
		}
		description := marker.Description
		if description == "" {
			description = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s", prefix, formatMarkerPosition(marker.PositionSeconds), description))
	}
	return lines
}

func formatMarkerPosition(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

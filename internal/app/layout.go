package app

// This file holds the shell's layout pass. Everything the shell draws is
// sized once per frame, before any rendering happens, by shellModel.layout.
// Keeping that measurement apart from the drawing gives one answer to "how
// big is the chat pane?" that the renderer, the mouse hit tests, and the
// scroll clamps all share.

// shellLayout is the finished measurement of one frame: how many rows and
// columns each region of the window gets, and whether each pane is drawn with
// a border. Every view function reads these numbers instead of measuring the
// terminal itself, so the panes agree on where their edges are.
//
// A "content" height excludes the pane's own border rows; a "framed" flag says
// whether that border is drawn at all, which the narrowest terminals turn off.
type shellLayout struct {
	width                       int
	tabBarHeight                int
	statusHeight                int
	chatHeight                  int
	chatContentHeight           int
	chatFramed                  bool
	chatWidth                   int
	sidebarWidth                int
	sidebarContentHeight        int
	activityWidth               int
	activityContentHeight       int
	inspectHeight               int
	inspectContentHeight        int
	inspectFramed               bool
	paletteHeight               int
	paletteContentHeight        int
	paletteFramed               bool
	emotePickerHeight           int
	emotePickerContentHeight    int
	emotePickerFramed           bool
	channelPickerHeight         int
	channelPickerContentHeight  int
	channelPickerFramed         bool
	categoryPickerHeight        int
	categoryPickerContentHeight int
	categoryPickerFramed        bool
	streamInfoHeight            int
	streamInfoContentHeight     int
	streamInfoFramed            bool
	miscHeight                  int
	miscContentHeight           int
	miscFramed                  bool
	composerHeight              int
	composerFramed              bool
	helpHeight                  int
}

func (m shellModel) layout() shellLayout {
	width := clampMin(m.width, 1)
	height := clampMin(m.height, 1)
	layout := shellLayout{
		width:        width,
		chatWidth:    width,
		tabBarHeight: 1,
		statusHeight: 1,
		helpHeight:   1,
	}
	if height == 1 {
		layout.tabBarHeight = 0
		layout.helpHeight = 0
		return layout
	}

	if m.helpExpanded {
		// The expanded help has four lines; taller terminals show all of
		// them, shorter ones keep the most important prefix.
		switch {
		case height >= 18:
			layout.helpHeight = 4
		case height >= 14:
			layout.helpHeight = 3
		case height >= 10:
			layout.helpHeight = 2
		}
	}

	onStreamInfo := m.activeTab == tabStreamInfo
	onMisc := m.activeTab == tabMisc

	if !onStreamInfo && !onMisc {
		layout.composerHeight = 4
		if m.activeChannelState().replyTo != nil {
			layout.composerHeight++
		}
		layout.composerFramed = width >= 8
		if height < 10 {
			layout.composerHeight = 3
		}
	}

	remaining := height - layout.tabBarHeight - layout.statusHeight - layout.helpHeight - layout.composerHeight
	if remaining < 3 && layout.composerHeight > 3 {
		layout.composerHeight = 3
		remaining = height - layout.tabBarHeight - layout.statusHeight - layout.helpHeight - layout.composerHeight
	}
	if remaining < 1 && layout.helpHeight > 0 {
		layout.helpHeight = 0
		remaining = height - layout.tabBarHeight - layout.statusHeight - layout.composerHeight
	}
	if remaining < 1 && layout.composerHeight > 0 {
		layout.composerHeight = clampMin(height-layout.tabBarHeight-layout.statusHeight, 0)
		layout.composerFramed = layout.composerHeight >= 3 && width >= 8
		remaining = height - layout.tabBarHeight - layout.statusHeight - layout.composerHeight
	}

	// The overlay panes -- command palette, inspector, emote picker, channel
	// picker, category picker -- all occupy the same strip above the composer,
	// so at most one is ever sized. This switch is what decides which one wins
	// when several are somehow open at once; the order of the cases is that
	// priority. It replaces a chain of conditions in which each pane repeated
	// the negation of every pane above it, where adding a sixth overlay meant
	// correctly editing all five conditions before it.
	if remaining >= 4 {
		switch {
		case m.palette.open:
			pane := sizeOverlayPane(remaining, height, width, 7)
			layout.paletteHeight = pane.height
			layout.paletteFramed = pane.framed
			layout.paletteContentHeight = pane.contentHeight
			remaining -= pane.height
		case m.inspectOpen:
			pane := sizeOverlayPane(remaining, height, width, 7)
			layout.inspectHeight = pane.height
			layout.inspectFramed = pane.framed
			layout.inspectContentHeight = pane.contentHeight
			remaining -= pane.height
		case m.emotePicker.open:
			pane := sizeOverlayPane(remaining, height, width, 7)
			layout.emotePickerHeight = pane.height
			layout.emotePickerFramed = pane.framed
			layout.emotePickerContentHeight = pane.contentHeight
			remaining -= pane.height
		case m.channelPicker.open:
			// The channel picker lists channels rather than a few commands,
			// so it takes two extra rows where the terminal can spare them.
			pane := sizeOverlayPane(remaining, height, width, 9)
			layout.channelPickerHeight = pane.height
			layout.channelPickerFramed = pane.framed
			layout.channelPickerContentHeight = pane.contentHeight
			remaining -= pane.height
		case m.categoryPicker.open:
			pane := sizeOverlayPane(remaining, height, width, 7)
			layout.categoryPickerHeight = pane.height
			layout.categoryPickerFramed = pane.framed
			layout.categoryPickerContentHeight = pane.contentHeight
			remaining -= pane.height
		}
	}

	layout.chatHeight = clampMin(remaining, 0)
	layout.sidebarWidth = m.sidebarWidth(width, layout.chatHeight)
	layout.activityWidth = m.activityWidthFor(width, layout.chatHeight)
	layout.chatWidth = clampMin(width-layout.sidebarWidth-layout.activityWidth, 1)
	layout.chatFramed = layout.chatHeight >= 3 && width >= 5
	layout.applyChatContentHeights()

	used := layout.tabBarHeight + layout.statusHeight + layout.chatHeight + layout.paletteHeight + layout.inspectHeight + layout.emotePickerHeight + layout.channelPickerHeight + layout.categoryPickerHeight + layout.composerHeight + layout.helpHeight
	if used < height {
		layout.chatHeight += height - used
		layout.applyChatContentHeights()
	}

	switch {
	case onStreamInfo:
		body := layout.takeBodyFromChat(width)
		layout.streamInfoHeight = body.height
		layout.streamInfoContentHeight = body.contentHeight
		layout.streamInfoFramed = body.framed
	case onMisc:
		body := layout.takeBodyFromChat(width)
		layout.miscHeight = body.height
		layout.miscContentHeight = body.contentHeight
		layout.miscFramed = body.framed
	}

	return layout
}

// applyChatContentHeights recomputes every row count that follows from
// chatHeight: the chat pane's own content rows, and the sidebar and activity
// columns standing beside it. Call it again whenever chatHeight changes.
func (l *shellLayout) applyChatContentHeights() {
	l.chatContentHeight = l.chatHeight
	if l.chatFramed {
		l.chatContentHeight = l.chatHeight - 2
	}
	l.chatContentHeight = clampMin(l.chatContentHeight, 0)
	l.sidebarContentHeight = clampMin(l.chatHeight-2, 0)
	l.activityContentHeight = l.sidebarContentHeight
}

// bodyPane is the geometry of whatever pane occupies the body of the window
// between the tab bar and the composer.
type bodyPane struct {
	height        int
	contentHeight int
	framed        bool
}

// takeBodyFromChat hands the chat pane's geometry to a tab that draws across
// the full width of the body -- Stream Info and Misc -- and clears the chat
// pane along with the sidebar and activity columns, none of which those tabs
// show. It returns the geometry the calling tab has just taken over.
func (l *shellLayout) takeBodyFromChat(width int) bodyPane {
	body := bodyPane{
		height:        l.chatHeight,
		contentHeight: l.chatContentHeight,
		framed:        l.chatFramed,
	}
	l.sidebarWidth = 0
	l.activityWidth = 0
	l.chatWidth = width
	l.sidebarContentHeight = 0
	l.activityContentHeight = 0
	l.chatHeight = 0
	l.chatContentHeight = 0
	l.chatFramed = false
	return body
}

// overlayPaneSize is the vertical geometry of one overlay pane: how many
// rows it occupies, whether it is drawn with a border, and how many rows are
// left for its contents once that border is accounted for.
type overlayPaneSize struct {
	height        int
	framed        bool
	contentHeight int
}

// sizeOverlayPane sizes one of the overlay panes that share the strip above
// the composer.
//
// remaining is the rows still unclaimed by the panes around it, height and
// width are the terminal's, and tallHeight is the size the pane grows to when
// the terminal is tall enough to afford it. The pane always leaves at least
// one row for the chat behind it, and collapses to nothing rather than
// rendering shorter than the three rows a bordered pane needs.
func sizeOverlayPane(remaining, height, width, tallHeight int) overlayPaneSize {
	paneHeight := 5
	if height >= 18 {
		paneHeight = tallHeight
	}
	if paneHeight > remaining-1 {
		paneHeight = remaining - 1
	}
	if paneHeight < 3 {
		paneHeight = 0
	}
	pane := overlayPaneSize{
		height:        paneHeight,
		framed:        paneHeight >= 3 && width >= 5,
		contentHeight: paneHeight,
	}
	if pane.framed {
		pane.contentHeight = paneHeight - 2
	}
	return pane
}

func (m shellModel) sidebarWidth(width, chatHeight int) int {
	if !m.sidebarVisibleFor(width, chatHeight) {
		return 0
	}
	fallback := sidebarNormalSize
	if width >= 112 {
		fallback = sidebarWideSize
	}
	// The activity column is measured first and passed in as the competing
	// pane, so the two can never together starve chat.
	return paneWidthOrDefault(
		m.panes.sidebarWidthOverride, fallback,
		sidebarMinSize, sidebarMaxSize,
		width, m.activityWidthFor(width, chatHeight),
	)
}

// activityWidthFor decides the right-hand activity log column's width.
// Unlike the left channel sidebar, it doesn't need multiple channels to be
// useful (raids/subs/new followers matter even with one channel open), so
// it's gated only on having enough width and chat vertical room.
func (m shellModel) activityWidthFor(width, chatHeight int) int {
	if !m.activityVisibleFor(width, chatHeight) {
		return 0
	}
	fallback := activityLogNormalSize
	if width >= 140 {
		fallback = activityLogWideSize
	}
	return paneWidthOrDefault(
		m.panes.activityWidthOverride, fallback,
		activityMinSize, activityMaxSize,
		width, 0,
	)
}

func (m shellModel) chatRowWidth(layout shellLayout) int {
	rowWidth := layout.chatWidth
	if layout.chatFramed {
		rowWidth = layout.chatWidth - 4
	}
	return clampMin(rowWidth, 1)
}

func (m shellModel) chatMessageContentWidth(layout shellLayout) int {
	rowWidth := m.chatRowWidth(layout)
	return clampMin(rowWidth-messageGutterWidth(rowWidth), 1)
}

func messageGutterWidth(rowWidth int) int {
	switch {
	case rowWidth >= 24:
		return 4
	case rowWidth >= 12:
		return 2
	default:
		return 0
	}
}

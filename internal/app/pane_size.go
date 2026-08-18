package app

// Pane visibility and sizing for the two side columns.
//
// The channel sidebar and the activity column both borrow width from chat,
// which is the pane that matters most, so both are sized responsively by
// default and both can be overridden. The defaults suit a terminal nobody
// has thought about; the overrides exist because a streamer with a fixed
// layout has usually thought about it quite a lot.

// activityVisibility is the user's standing choice for the activity column,
// matching sidebarVisibility. Auto shows it whenever the terminal is wide
// enough, since raids, subs and follows are worth seeing even with a single
// channel open.
type activityVisibility int

const (
	activityAuto activityVisibility = iota
	activityShown
	activityHidden
)

// Side panes are clamped rather than rejected: a configured or resized width
// the terminal cannot afford degrades to the nearest usable value instead of
// producing a layout with no chat column in it.
const (
	sidebarMinSize  = 12
	sidebarMaxSize  = 40
	activityMinSize = 16
	activityMaxSize = 60
	// paneResizeStep is how much one keypress moves a pane edge. Two cells
	// is small enough to land on a specific width without feeling slow.
	paneResizeStep = 2
	// minChatWidthAfterPanes is the chat column the side panes must leave
	// behind. Below this, messages wrap into an unreadable ribbon, so a
	// resize stops here rather than honoring the request.
	minChatWidthAfterPanes = 32
)

// toggleActivity flips the activity column between shown and hidden,
// starting from whatever the layout currently shows so the first press
// always does the visible thing regardless of what auto had decided. This
// mirrors toggleSidebar exactly; the two panes should not behave differently.
func (m *shellModel) toggleActivity() {
	if m.layout().activityWidth > 0 {
		m.activityVisibility = activityHidden
		return
	}
	m.activityVisibility = activityShown
}

// activityVisibleFor decides whether the activity column has room and reason
// to be drawn. Width and chat height are hard constraints in every mode, as
// they are for the sidebar: a column that would leave no usable chat helps
// nobody, and an explicit "show" cannot conjure space that is not there.
func (m shellModel) activityVisibleFor(width, chatHeight int) bool {
	if chatHeight < 3 {
		return false
	}
	switch m.activityVisibility {
	case activityShown:
		// A deliberate show still needs enough width to leave chat readable,
		// but does not require the auto threshold: someone who asked for the
		// column on a 90-column terminal should get it.
		return width >= minChatWidthAfterPanes+activityMinSize
	case activityHidden:
		return false
	default:
		return width >= activityLogMinWidth
	}
}

// resizeSidebar and resizeActivity move a pane edge by delta cells.
//
// They set an explicit override rather than nudging the responsive default,
// so a resized pane keeps the width the user chose when the terminal is
// resized, instead of springing back at the next breakpoint. Passing through
// the current effective width means the first press moves from what is on
// screen, not from whatever the config happened to say.
func (m *shellModel) resizeSidebar(delta int) {
	layout := m.layout()
	if layout.sidebarWidth <= 0 {
		return
	}
	m.sidebarWidthOverride = clampPaneWidth(
		layout.sidebarWidth+delta, sidebarMinSize, sidebarMaxSize,
		layout.width, layout.activityWidth,
	)
}

func (m *shellModel) resizeActivity(delta int) {
	layout := m.layout()
	if layout.activityWidth <= 0 {
		return
	}
	m.activityWidthOverride = clampPaneWidth(
		layout.activityWidth+delta, activityMinSize, activityMaxSize,
		layout.width, layout.sidebarWidth,
	)
}

// resizeFocusedPane routes a resize to whichever side pane the user is
// looking at. The sidebar is only resizable while it holds focus; otherwise
// the keys act on the activity column, which is the pane most people mean
// when they want more or less room for chat.
func (m *shellModel) resizeFocusedPane(delta int) {
	if m.focus == focusSidebar {
		m.resizeSidebar(delta)
		return
	}
	m.resizeActivity(delta)
}

// resetPaneWidths drops both overrides, returning the panes to sizing
// themselves from the terminal width.
func (m *shellModel) resetPaneWidths() {
	m.sidebarWidthOverride = 0
	m.activityWidthOverride = 0
}

// clampPaneWidth keeps a pane inside its own bounds and inside what the
// terminal can spare once the other side pane has taken its share.
func clampPaneWidth(want, minimum, maximum, totalWidth, otherPaneWidth int) int {
	if want < minimum {
		want = minimum
	}
	if want > maximum {
		want = maximum
	}
	affordable := totalWidth - otherPaneWidth - minChatWidthAfterPanes
	if want > affordable {
		want = affordable
	}
	if want < minimum {
		// The terminal cannot afford even the minimum; leave the pane at its
		// floor and let the layout decide to drop it entirely.
		return minimum
	}
	return want
}

// paneWidthOrDefault resolves an override against the responsive default,
// applying the same clamps a resize would.
//
// The fallback goes through clampPaneWidth too, not only the override. The
// responsive default is picked from the terminal width alone, so on a narrow
// terminal it can be wider than the visibility guard reserved room for: an
// explicitly shown activity column is admitted from 48 columns (chat floor
// plus activityMinSize) but its default width is 28, which would leave chat
// at 20. Clamping the default degrades it to what the terminal can spare --
// 16 at width 48 -- so chat keeps minChatWidthAfterPanes either way.
func paneWidthOrDefault(override, fallback, minimum, maximum, totalWidth, otherPaneWidth int) int {
	want := fallback
	if override > 0 {
		want = override
	}
	return clampPaneWidth(want, minimum, maximum, totalWidth, otherPaneWidth)
}

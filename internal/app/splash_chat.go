package app

import (
	"strings"

	"github.com/rivo/uniseg"
	"github.com/worxbend/twi/internal/theme"
)

// The splash shows a tiny chat of its own while twi starts up.
//
// A progress bar tells you something is happening; it does not tell you what
// the thing you are starting actually does. A few characters talking to each
// other does both, using the same per-author color hashing and the same
// right-of-name layout that real chat uses, so the splash is a preview rather
// than unrelated decoration.
//
// Everything here is a pure function of the splash fraction. No extra model
// state, no second clock, and every frame is reproducible in a test.

// splashMascot is one participant in the startup chat.
type splashMascot struct {
	Name string
	Face string
}

// splashMascots are deliberately kaomoji rather than emoji: they render at a
// predictable width in every terminal twi supports, including the ones that
// draw emoji at one cell and break alignment everywhere else.
var splashMascots = []splashMascot{
	{Name: "bitbuddy", Face: "(•‿•)"},
	{Name: "pixelcat", Face: "(=^･ω･^=)"},
	{Name: "moddington", Face: "ʕ•ᴥ•ʔ"},
	{Name: "lurkbot", Face: "(¬‿¬)"},
	{Name: "raidgoose", Face: "(°□°)"},
}

// splashChatLine is one scripted message.
type splashChatLine struct {
	mascot int
	text   string
}

// splashChatScript reads as a channel warming up: someone says hello, someone
// lurks, a raid lands. It is short enough to finish inside the splash and
// generic enough not to imply anything about the user's own channel.
var splashChatScript = []splashChatLine{
	{0, "hey chat 👋"},
	{1, "nyaa~ terminal gang"},
	{2, "keeping it civil in here"},
	{3, "just lurking, as is tradition"},
	{4, "RAID INCOMING"},
	{0, "gg that was fast"},
	{1, "no browser tab in sight"},
}

const (
	// splashChatStart and splashChatEnd bound the chat inside the splash, so
	// the logo lands first and the last line is readable before the dashboard
	// replaces it.
	splashChatStart = 0.14
	splashChatEnd   = 0.94
	// splashChatTypeShare is how much of each message's slot is spent typing
	// it in. The remainder holds it steady, which is what makes the sequence
	// readable rather than a blur.
	splashChatTypeShare = 0.55
	// splashChatMaxRows caps how much vertical room the chat may claim.
	splashChatMaxRows = 5
)

// splashChatRows renders the messages revealed by now, newest last, already
// styled and padded to width. It returns nothing when there is no room, so
// the caller can lay out around it.
func (m shellModel) splashChatRows(fraction float64, width, maxRows int) []string {
	if width < 18 || maxRows <= 0 {
		return nil
	}
	revealed, typing := splashChatProgress(fraction)
	if revealed == 0 {
		return nil
	}

	// Show the tail: the chat scrolls like the real thing rather than
	// growing until it runs out of screen.
	first := 0
	if revealed > maxRows {
		first = revealed - maxRows
	}

	canvas := m.canvasBackground()
	rows := make([]string, 0, revealed-first)
	for i := first; i < revealed; i++ {
		line := splashChatScript[i]
		text := line.text
		if i == revealed-1 && typing < 1 {
			text = truncateToFraction(text, typing)
		}
		rows = append(rows, m.splashChatRow(splashMascots[line.mascot], text, width, canvas, i == revealed-1 && typing < 1))
	}
	return rows
}

// splashChatRow lays out one message as "face name: text", coloring the face
// and name with the same hash real chatters get, so a familiar chat line is
// what a new user sees first.
func (m shellModel) splashChatRow(mascot splashMascot, text string, width int, canvas string, typing bool) string {
	color := theme.IdentityColor(mascot.Name, []string{m.theme.Background, m.theme.Surface}, m.theme.Accent)

	head := mascot.Face + " " + mascot.Name
	if typing {
		text += "▍"
	}

	// Drop the face first, then shorten the name, before touching the
	// message: what was said matters more than who has room to be drawn.
	for _, candidate := range []string{head, mascot.Name} {
		if uniseg.StringWidth(candidate+": "+text) <= width {
			head = candidate
			break
		}
		head = mascot.Name
	}

	styledHead := splashStyledLine(head, color, canvas, true)
	styledSep := splashStyledLine(": ", m.theme.Muted, canvas, false)
	body := fitLine(text, clampMin(width-uniseg.StringWidth(head)-2, 1))
	styledBody := splashStyledLine(body, m.theme.Foreground, canvas, false)

	plainWidth := uniseg.StringWidth(head) + 2 + uniseg.StringWidth(body)
	pad := ""
	if plainWidth < width {
		pad = splashStyledLine(strings.Repeat(" ", width-plainWidth), m.theme.Foreground, canvas, false)
	}
	return styledHead + styledSep + styledBody + pad
}

// splashChatProgress maps the splash fraction onto "how many messages are on
// screen" and "how far through typing the newest one is".
func splashChatProgress(fraction float64) (revealed int, typing float64) {
	if fraction < splashChatStart {
		return 0, 0
	}
	span := splashChatEnd - splashChatStart
	if span <= 0 {
		return len(splashChatScript), 1
	}
	progress := (fraction - splashChatStart) / span
	if progress >= 1 {
		return len(splashChatScript), 1
	}

	slot := 1.0 / float64(len(splashChatScript))
	index := int(progress / slot)
	if index >= len(splashChatScript) {
		return len(splashChatScript), 1
	}
	within := (progress - float64(index)*slot) / slot
	typing = clampFraction(within / splashChatTypeShare)
	return index + 1, typing
}

// truncateToFraction returns the leading portion of text, measured in
// grapheme clusters so a multi-byte character is never split in half.
func truncateToFraction(text string, fraction float64) string {
	var clusters []string
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		clusters = append(clusters, graphemes.Str())
	}
	if len(clusters) == 0 {
		return ""
	}
	keep := int(clampFraction(fraction) * float64(len(clusters)))
	if keep >= len(clusters) {
		return text
	}
	return strings.Join(clusters[:keep], "")
}

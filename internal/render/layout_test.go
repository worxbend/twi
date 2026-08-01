package render

import (
	"strings"
	"testing"
	"time"

	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

func layoutTestMessage() twitch.ChatMessage {
	return twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		AuthorLogin: "alice_l",
		DisplayName: "Alice_L",
		Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
		Type:        twitch.MessageTypeChat,
		Text:        "hello chat",
	}
}

func TestNormalizeLayoutModeFallsBackToDefault(t *testing.T) {
	for _, test := range []struct {
		in   string
		want LayoutMode
	}{
		{"inline", LayoutInline},
		{"GROUPED", LayoutGrouped},
		{" compact ", LayoutCompact},
		{"", DefaultLayoutMode},
		{"nonsense", DefaultLayoutMode},
	} {
		if got := NormalizeLayoutMode(test.in); got != test.want {
			t.Errorf("NormalizeLayoutMode(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestGroupedLayoutPutsAuthorOnItsOwnRow(t *testing.T) {
	opts := DefaultOptions(72)
	opts.Layout = LayoutGrouped

	rows := rowsToPlain(Rows(layoutTestMessage(), opts))
	if len(rows) != 2 {
		t.Fatalf("grouped rows = %d (%#v), want a header row plus a body row", len(rows), rows)
	}
	if !strings.Contains(rows[0], "Alice_L") {
		t.Fatalf("header row = %q, want the author name", rows[0])
	}
	if strings.Contains(rows[0], "hello chat") {
		t.Fatalf("header row = %q, want message text on its own row", rows[0])
	}
	if !strings.Contains(rows[1], "hello chat") {
		t.Fatalf("body row = %q, want the message text", rows[1])
	}
	// The body is indented under the header so a run of messages reads as one
	// block rather than a flat list.
	if !strings.HasPrefix(rows[1], "   ") {
		t.Fatalf("body row = %q, want it indented under the author header", rows[1])
	}
}

func TestGroupedLayoutSuppressesRepeatedAuthorHeader(t *testing.T) {
	opts := DefaultOptions(72)
	opts.Layout = LayoutGrouped
	opts.ContinuesGroup = true

	rows := rowsToPlain(Rows(layoutTestMessage(), opts))
	if len(rows) != 1 {
		t.Fatalf("continued-group rows = %d (%#v), want just the body row", len(rows), rows)
	}
	if strings.Contains(rows[0], "Alice_L") {
		t.Fatalf("continued group repeated the author header: %q", rows[0])
	}
	if !strings.Contains(rows[0], "hello chat") {
		t.Fatalf("continued-group row = %q, want the message text", rows[0])
	}
}

func TestCompactLayoutDropsDecorations(t *testing.T) {
	opts := DefaultOptions(72)
	opts.Layout = LayoutCompact
	opts.Assets = FallbackAssetOptions()

	rows := rowsToPlain(Rows(layoutTestMessage(), opts))
	if len(rows) != 1 {
		t.Fatalf("compact rows = %d (%#v), want one", len(rows), rows)
	}
	if got, want := rows[0], "Alice_L: hello chat"; got != want {
		t.Fatalf("compact row = %q, want %q", got, want)
	}
}

func TestBadgeModesRenderBadgesDifferently(t *testing.T) {
	msg := layoutTestMessage()
	for _, test := range []struct {
		mode        BadgeMode
		wantContain string
		wantAbsent  string
	}{
		{mode: BadgeModeGlyph, wantContain: "⚔", wantAbsent: "[mod"},
		{mode: BadgeModeText, wantContain: "[mod", wantAbsent: "⚔"},
		{mode: BadgeModeOff, wantContain: "Alice_L", wantAbsent: "⚔"},
	} {
		opts := DefaultOptions(72)
		opts.Badges = test.mode
		row := strings.Join(rowsToPlain(Rows(msg, opts)), "\n")
		if !strings.Contains(row, test.wantContain) {
			t.Errorf("badge mode %q row = %q, want it to contain %q", test.mode, row, test.wantContain)
		}
		if strings.Contains(row, test.wantAbsent) {
			t.Errorf("badge mode %q row = %q, want it to omit %q", test.mode, row, test.wantAbsent)
		}
	}
}

func TestBadgeModeOffAlsoDropsBadgesInGroupedHeader(t *testing.T) {
	opts := DefaultOptions(72)
	opts.Layout = LayoutGrouped
	opts.Badges = BadgeModeOff

	header := rowsToPlain(Rows(layoutTestMessage(), opts))[0]
	if strings.Contains(header, "⚔") || strings.Contains(header, "[mod") {
		t.Fatalf("grouped header = %q, want no badges when badges are off", header)
	}
}

func TestFullUsernameAppendsLoginOnlyWhenItDiffers(t *testing.T) {
	opts := DefaultOptions(72)
	opts.FullUsername = true

	// Display name differs from the login by more than case, so the login is
	// shown to keep the account mentionable.
	msg := layoutTestMessage()
	msg.DisplayName = "アリス"
	msg.AuthorLogin = "alice_l"
	if got := rowsToPlain(Rows(msg, opts))[0]; !strings.Contains(got, "アリス (alice_l)") {
		t.Fatalf("row = %q, want the login appended to a differing display name", got)
	}

	// A pure recapitalization adds nothing, so it is not repeated.
	msg.DisplayName = "Alice_L"
	if got := rowsToPlain(Rows(msg, opts))[0]; strings.Contains(got, "(alice_l)") {
		t.Fatalf("row = %q, want no redundant login for a recapitalized display name", got)
	}
}

func TestHighlightEmotesTintsEmoteAndEmojiBackgrounds(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "alice",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "hi "},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: "😀"},
		},
	}

	opts := DefaultOptions(72)
	opts.HighlightEmotes = true
	rows := Rows(msg, opts)

	emote, ok := firstKind(rows, FragmentEmoteFallback)
	if !ok {
		t.Fatal("rows missing an emote fragment")
	}
	emojiFragment, ok := firstKind(rows, FragmentEmojiFallback)
	if !ok {
		t.Fatal("rows missing an emoji fragment")
	}
	if emote.Style.Background == "" || emojiFragment.Style.Background == "" {
		t.Fatalf("highlight left backgrounds unset: emote=%q emoji=%q", emote.Style.Background, emojiFragment.Style.Background)
	}
	// Emotes and emoji get distinct tints so the two are still tellable apart.
	if emote.Style.Background == emojiFragment.Style.Background {
		t.Fatalf("emote and emoji share background %q, want distinct tints", emote.Style.Background)
	}
	// Plain text must stay on the pane background rather than becoming a chip.
	text, ok := firstKind(rows, FragmentText)
	if ok && text.Style.Background != "" {
		t.Fatalf("plain text picked up a highlight background %q", text.Style.Background)
	}

	opts.HighlightEmotes = false
	rows = Rows(msg, opts)
	if emote, _ = firstKind(rows, FragmentEmoteFallback); emote.Style.Background != "" {
		t.Fatalf("highlighting disabled but emote background = %q", emote.Style.Background)
	}
}

func TestAuthorMetaRendersOnlyKnownFacts(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	opts := DefaultOptions(100)
	opts.Layout = LayoutGrouped
	opts.Meta = AuthorMeta{
		Role:             "mod",
		SubscribedMonths: 26,
		FollowsSince:     now.Add(-90 * 24 * time.Hour),
		FollowKnown:      true,
		FirstSeen:        now.Add(-2 * time.Hour),
		Now:              now,
	}

	header := rowsToPlain(Rows(layoutTestMessage(), opts))[0]
	for _, want := range []string{"mod", "sub 26mo", "follows 3mo", "seen 2h"} {
		if !strings.Contains(header, want) {
			t.Errorf("grouped header = %q, want it to contain %q", header, want)
		}
	}
}

func TestAuthorMetaOmitsUnknownFollowState(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	opts := DefaultOptions(100)
	opts.Layout = LayoutGrouped
	// FollowKnown is false: twi has no follower data for this user. That must
	// never be rendered as "does not follow".
	opts.Meta = AuthorMeta{Role: "vip", FirstSeen: now.Add(-time.Hour), Now: now}

	header := rowsToPlain(Rows(layoutTestMessage(), opts))[0]
	if strings.Contains(header, "follow") {
		t.Fatalf("grouped header = %q, want no follow claim when follower data is unknown", header)
	}
	if !strings.Contains(header, "vip") {
		t.Fatalf("grouped header = %q, want the known role", header)
	}
}

func TestEmptyAuthorMetaRendersNothing(t *testing.T) {
	opts := DefaultOptions(100)
	opts.Layout = LayoutGrouped

	header := rowsToPlain(Rows(layoutTestMessage(), opts))[0]
	if strings.Contains(header, "·") {
		t.Fatalf("grouped header = %q, want no metadata separator with empty meta", header)
	}
}

func TestHumanizeDurationPicksOneUnit(t *testing.T) {
	for _, test := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "0m"},
		{45 * time.Minute, "45m"},
		{5 * time.Hour, "5h"},
		{10 * 24 * time.Hour, "10d"},
		{90 * 24 * time.Hour, "3mo"},
		{800 * 24 * time.Hour, "2y"},
		{-time.Hour, "0m"},
	} {
		if got := humanizeDuration(test.in); got != test.want {
			t.Errorf("humanizeDuration(%v) = %q, want %q", test.in, got, test.want)
		}
	}
}

// Every layout must fit its width budget at any terminal size; a row wider
// than the pane corrupts the frame rather than merely looking wrong.
func TestAllLayoutsRespectWidthBudget(t *testing.T) {
	msg := layoutTestMessage()
	msg.Text = "a much longer message that will certainly need to wrap somewhere"
	msg.DisplayName = "AVeryLongDisplayNameIndeed"

	for _, layout := range []LayoutMode{LayoutInline, LayoutGrouped, LayoutCompact} {
		for width := minimumRenderWidth; width <= 100; width++ {
			opts := Options{
				Width:           width,
				Palette:         theme.DefaultPalette(),
				Assets:          FallbackAssetOptions(),
				Layout:          layout,
				HighlightEmotes: true,
				FullUsername:    true,
			}
			for _, row := range Rows(msg, opts) {
				if got := row.Width(); got > width {
					t.Fatalf("layout %q at width %d produced a %d-cell row: %q", layout, width, got, row.Plain())
				}
			}
		}
	}
}

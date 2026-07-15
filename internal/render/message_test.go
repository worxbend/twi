package render

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

// forceColorProfile pins lipgloss's default renderer to TrueColor for the
// duration of the test and restores whatever profile was active before.
// Setting env vars alone isn't reliable here: lipgloss/termenv detect and
// cache the profile once per process, so whichever test in this package
// first touches lipgloss rendering can lock in "no color" for every test
// that runs after it in the same `go test` binary.
func forceColorProfile(t *testing.T) {
	t.Helper()
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(original)
	})
}

func TestFragmentWithDefaultBackgroundFillsOnlyWhenUnset(t *testing.T) {
	withoutBackground := Fragment{Kind: FragmentText, Text: "hi", Style: FragmentStyle{Foreground: "#ffffff"}}
	filled := fragmentWithDefaultBackground(withoutBackground, "#111018")
	if filled.Style.Background != "#111018" {
		t.Fatalf("Background = %q, want #111018", filled.Style.Background)
	}
	if withoutBackground.Style.Background != "" {
		t.Fatal("fragmentWithDefaultBackground mutated the original fragment")
	}

	withBackground := Fragment{Kind: FragmentText, Text: "hi", Style: FragmentStyle{Background: "#9146ff"}}
	unchanged := fragmentWithDefaultBackground(withBackground, "#111018")
	if unchanged.Style.Background != "#9146ff" {
		t.Fatalf("Background = %q, want existing #9146ff preserved", unchanged.Style.Background)
	}
}

// backgroundOnlySGRCode renders a single Background-only fragment to learn
// exactly which SGR code this test environment's detected color profile
// (truecolor, 256-color, or ANSI16) produces for hex, so assertions below
// don't hardcode a specific profile's downsampled color code.
func backgroundOnlySGRCode(t *testing.T, hex string) string {
	t.Helper()
	ref := Row{Fragments: []Fragment{{Kind: FragmentText, Text: "x", Style: FragmentStyle{Background: hex}}}}
	out := ref.TerminalString()
	start := strings.Index(out, "\x1b[")
	end := strings.Index(out, "m")
	if start < 0 || end < 0 || end <= start+2 {
		t.Fatalf("could not parse an SGR sequence out of %q", out)
	}
	return out[start+2 : end]
}

func TestTerminalStringWithBackgroundAppliesPastEmbeddedResets(t *testing.T) {
	forceColorProfile(t)
	background := "#111018"
	backgroundCode := backgroundOnlySGRCode(t, background)

	row := Row{Fragments: []Fragment{
		{Kind: FragmentText, Text: "red", Style: FragmentStyle{Foreground: "#ff0000"}},
		{Kind: FragmentText, Text: " plain"},
		{Kind: FragmentUsername, Text: "green", Style: FragmentStyle{Foreground: "#00ff00"}},
	}}

	// Plain TerminalString leaves every fragment's background empty, so
	// each fragment's own SGR reset ends any background coloring — this is
	// the exact bug: only text before the *first* reset would ever show a
	// background if the whole row were later wrapped in an outer style.
	plain := row.TerminalString()
	if strings.Contains(plain, backgroundCode+"m") {
		t.Fatalf("TerminalString() unexpectedly contains the background SGR code %q: %q", backgroundCode, plain)
	}

	// TerminalStringWithBackground must apply the background to every
	// fragment independently, so it survives each fragment's own reset.
	withBg := row.TerminalStringWithBackground(background)
	backgroundCount := strings.Count(withBg, backgroundCode+"m")
	if backgroundCount < 3 {
		t.Fatalf("TerminalStringWithBackground(%s) applied background %d times, want at least 3 (once per fragment):\n%q", background, backgroundCount, withBg)
	}
}

func TestTerminalStringWithBackgroundPreservesExplicitFragmentBackground(t *testing.T) {
	forceColorProfile(t)
	defaultCode := backgroundOnlySGRCode(t, "#111018")
	explicitCode := backgroundOnlySGRCode(t, "#9146ff")

	row := Row{Fragments: []Fragment{
		{Kind: FragmentText, Text: "AB", Style: FragmentStyle{Background: "#9146ff"}},
	}}
	out := row.TerminalStringWithBackground("#111018")
	if strings.Contains(out, defaultCode+"m") {
		t.Fatalf("TerminalStringWithBackground overrode an explicit fragment background: %q", out)
	}
	if !strings.Contains(out, explicitCode+"m") {
		t.Fatalf("TerminalStringWithBackground dropped the explicit fragment background: %q", out)
	}
}

func TestRowsSnapshotNormalWidth(t *testing.T) {
	now := time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local)

	tests := []struct {
		name string
		msg  twitch.ChatMessage
		want []string
	}{
		{
			name: "normal with fragments",
			msg: twitch.ChatMessage{
				Timestamp:   now,
				DisplayName: "alice",
				AuthorColor: "#222222",
				Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
				Type:        twitch.MessageTypeChat,
				Fragments: []twitch.MessageFragment{
					{Type: twitch.FragmentText, Text: "hello "},
					{Type: twitch.FragmentMention, Text: "@bob"},
					{Type: twitch.FragmentText, Text: " "},
					{Type: twitch.FragmentEmoji, Text: "😀"},
					{Type: twitch.FragmentText, Text: " "},
					{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
				},
			},
			want: []string{"20:00 [moderator] alice: hello @bob 😀 Kappa"},
		},
		{
			name: "reply",
			msg: twitch.ChatMessage{
				Timestamp:   now.Add(time.Minute),
				DisplayName: "carol",
				Type:        twitch.MessageTypeChat,
				Text:        "thanks for the context",
				Reply: &twitch.Reply{
					ParentAuthor: "bob",
					ParentText:   "original text",
				},
			},
			want: []string{"20:01 carol: reply to bob: original text thanks for the context"},
		},
		{
			name: "action",
			msg: twitch.ChatMessage{
				Timestamp:   now.Add(2 * time.Minute),
				DisplayName: "dancer",
				Type:        twitch.MessageTypeAction,
				Text:        "waves at chat",
			},
			want: []string{"20:02 * dancer waves at chat"},
		},
		{
			name: "notice",
			msg: twitch.ChatMessage{
				Timestamp: now.Add(3 * time.Minute),
				Type:      twitch.MessageTypeNotice,
				Text:      "scheduled maintenance",
			},
			want: []string{"20:03 notice: [notice] scheduled maintenance"},
		},
		{
			name: "deleted",
			msg: twitch.ChatMessage{
				Timestamp:   now.Add(4 * time.Minute),
				DisplayName: "mod",
				Type:        twitch.MessageTypeChat,
				Deleted:     true,
				Text:        "removed text",
			},
			want: []string{"20:04 mod: [message deleted]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := plainRows(tt.msg, 72); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestRowsSnapshotNarrowWrapping(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "longviewer",
		Type:        twitch.MessageTypeChat,
		Text:        "one two three four five six",
	}

	want := []string{
		"20:00 longviewer: one two th",
		"                  ree four f",
		"                  ive six",
	}

	got := plainRows(msg, 28)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
	for _, row := range Rows(msg, DefaultOptions(28)) {
		if !utf8.ValidString(row.Plain()) {
			t.Fatalf("row is invalid UTF-8: %q", row.Plain())
		}
		if row.Width() > 28 {
			t.Fatalf("row width = %d, want <= 28: %q", row.Width(), row.Plain())
		}
	}
}

func TestRowsPreserveFullUsernameWhenNarrow(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "exceptionally_long_viewer_name",
		Type:        twitch.MessageTypeChat,
		Text:        "hello chat",
	}

	rows := Rows(msg, DefaultOptions(18))
	plain := rowsToPlain(rows)
	joined := strings.Join(plain, "")
	if !strings.Contains(joined, "exceptionally_long_viewer_name") {
		t.Fatalf("rows truncated username\nrows: %#v", plain)
	}
	if !strings.Contains(strings.ReplaceAll(joined, " ", ""), ":hellochat") {
		t.Fatalf("rows missing message content\nrows: %#v", plain)
	}
	for _, row := range rows {
		if row.Width() > 18 {
			t.Fatalf("row width = %d, want <= 18: %q", row.Width(), row.Plain())
		}
	}
}

func TestRowsUseEmoteTokenFallbacksWithoutFragments(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "viewer",
		Text:        "hello Kappa there",
		Type:        twitch.MessageTypeChat,
		Emotes:      []twitch.Emote{{ID: "25", Name: "Kappa", Start: 6, End: 10}},
	}

	rows := Rows(msg, DefaultOptions(80))
	if got, want := rows[0].Plain(), "20:00 viewer: hello Kappa there"; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	if !hasKind(rows, FragmentEmoteFallback) {
		t.Fatalf("rows missing %s fragment: %#v", FragmentEmoteFallback, rows)
	}
}

func TestRowsDeriveEmoteFragmentIDFromTwitchCDNURL(t *testing.T) {
	rawURL := "https://static-cdn.jtvnw.net/emoticons/v2/emotesv2_299397e0339249f8a1b50f0affb044d8/default/dark/1.0#e=0"
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "viewer",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "sent "},
			{Type: twitch.FragmentEmote, Text: "Party", Ref: twitch.AssetRef{Kind: "twitch_emote", URL: rawURL}},
		},
	}
	opts := DefaultOptions(80)
	opts.Assets = FallbackAssetOptions()

	rows := Rows(msg, opts)

	emote, ok := firstKind(rows, FragmentEmoteFallback)
	if !ok {
		t.Fatalf("rows missing emote fragment: %#v", rows)
	}
	if got, want := emote.Ref.ID, "emotesv2_299397e0339249f8a1b50f0affb044d8"; got != want {
		t.Fatalf("emote ref ID = %q, want %q", got, want)
	}
	if got := emote.Ref.URL; got != rawURL {
		t.Fatalf("emote ref URL = %q, want original fragment URL %q", got, rawURL)
	}
}

func TestRowsSortEmoteTokenFallbacksByRange(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "viewer",
		Text:        "Kappa hi Keepo",
		Type:        twitch.MessageTypeChat,
		Emotes: []twitch.Emote{
			{ID: "1902", Name: "Keepo", Start: 9, End: 13},
			{ID: "25", Name: "Kappa", Start: 0, End: 4},
		},
	}

	rows := Rows(msg, DefaultOptions(80))
	if got, want := rows[0].Plain(), "20:00 viewer: Kappa hi Keepo"; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	if got, want := countKind(rows, FragmentEmoteFallback), 2; got != want {
		t.Fatalf("emote fallback count = %d, want %d: %#v", got, want, rows)
	}
}

func TestRowsKeepTokenFragmentsAtomicWhenWrapping(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "viewer",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "hello x "},
			{Type: twitch.FragmentMention, Text: "@bob"},
			{Type: twitch.FragmentText, Text: " xx "},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
		},
	}

	rows := Rows(msg, DefaultOptions(25))
	want := []string{
		"20:00 viewer: hello x ",
		"              @bob xx ",
		"              Kappa",
	}
	if got := rowsToPlain(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if mention, ok := firstKind(rows, FragmentMention); !ok || mention.Text != "@bob" {
		t.Fatalf("mention fragment = %#v, %v; want whole @bob", mention, ok)
	}
	if emote, ok := firstKind(rows, FragmentEmoteFallback); !ok || emote.Text != "Kappa" {
		t.Fatalf("emote fragment = %#v, %v; want whole Kappa", emote, ok)
	}
}

func TestRowsExposeRepresentativeFragmentKinds(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "alice",
		AuthorColor: "#111111",
		Badges:      []twitch.Badge{{SetID: "vip", ID: "1"}},
		Type:        twitch.MessageTypeNotice,
		Text:        "@bob 😀",
		Reply:       &twitch.Reply{ParentAuthor: "mod", ParentText: "please check"},
	}

	rows := Rows(msg, DefaultOptions(80))
	for _, kind := range []FragmentKind{
		FragmentTimestamp,
		FragmentBadge,
		FragmentUsername,
		FragmentText,
		FragmentMention,
		FragmentReply,
		FragmentNotice,
		FragmentEmojiFallback,
	} {
		if !hasKind(rows, kind) {
			t.Fatalf("rows missing %s fragment: %#v", kind, rows)
		}
	}

	username, ok := firstKind(rows, FragmentUsername)
	if !ok {
		t.Fatal("username fragment missing")
	}
	if got, want := username.Style.Foreground, theme.DefaultPalette().Foreground; got != want {
		t.Fatalf("username color = %q, want corrected fallback %q", got, want)
	}
	if styled := rows[0].String(); styled == "" || !strings.Contains(styled, "alice") {
		t.Fatalf("styled row missing username: %q", styled)
	}
}

func TestRowsExposeActionFragment(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "dancer",
		Type:        twitch.MessageTypeAction,
		Text:        "waves at chat",
	}

	rows := Rows(msg, DefaultOptions(80))
	if got, want := rows[0].Plain(), "20:00 * dancer waves at chat"; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	if !hasKind(rows, FragmentAction) {
		t.Fatalf("rows missing %s fragment: %#v", FragmentAction, rows)
	}
}

func TestRowsReserveStableAssetFallbackWidths(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		AuthorID:    "user-1",
		AvatarURL:   "https://static-cdn.example/avatar.png",
		DisplayName: "Alice_Liddell",
		AuthorColor: "#9146ff",
		Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentText, Text: "hello "},
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: "😀"},
		},
	}
	opts := DefaultOptions(80)
	opts.Assets = FallbackAssetOptions()

	rows := Rows(msg, opts)
	if got := rows[0].Plain(); !strings.Contains(got, "[AL] 20:00 [mod] ") {
		t.Fatalf("row missing intentional avatar/badge fallback: %q", got)
	}

	checks := []struct {
		kind FragmentKind
		want int
		ref  twitch.AssetRef
	}{
		{kind: FragmentAvatar, want: 5, ref: twitch.AssetRef{Kind: "avatar", ID: "user-1", URL: "https://static-cdn.example/avatar.png"}},
		{kind: FragmentBadge, want: 6, ref: twitch.AssetRef{Kind: "badge", ID: "moderator/1"}},
		{kind: FragmentEmoteFallback, want: 6, ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
		{kind: FragmentEmojiFallback, want: 2, ref: twitch.AssetRef{Kind: "emoji", ID: "1f600"}},
	}
	for _, check := range checks {
		fragment, ok := firstKind(rows, check.kind)
		if !ok {
			t.Fatalf("rows missing %s fragment: %#v", check.kind, rows)
		}
		if got := fragment.Width(); got != check.want {
			t.Fatalf("%s width = %d, want %d: %#v", check.kind, got, check.want, fragment)
		}
		if got := textWidth(fragmentFallbackText(fragment)); got != check.want {
			t.Fatalf("%s fallback display width = %d, want %d: %q", check.kind, got, check.want, fragmentFallbackText(fragment))
		}
		if fragment.Ref != check.ref {
			t.Fatalf("%s ref = %#v, want %#v", check.kind, fragment.Ref, check.ref)
		}
	}
}

func TestRowsImageOffAndAutoUnsupportedKeepTextFallbacksStable(t *testing.T) {
	msg := assetHeavyMessage()
	features := config.Default().Features
	features.AvatarMode = "image"
	features.EmojiMode = "image"
	features.EmoteMode = "image"

	unsupportedDecision := DecideImageCapabilities(features, DetectTerminalImageSignals([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), true)
	offFeatures := features
	offFeatures.ImageMode = "off"
	offDecision := DecideImageCapabilities(offFeatures, DetectTerminalImageSignals([]string{"TERM=xterm-kitty", "KITTY_WINDOW_ID=42", "COLORTERM=truecolor"}), true)

	want := []string{"[VF] 20:00 [moderator] viewer_fan: Kappa 😀"}
	for _, decision := range []ImageCapabilityDecision{unsupportedDecision, offDecision} {
		opts := DefaultOptions(80)
		opts.Assets = decision.AssetOptions()
		rows := Rows(msg, opts)
		if got := rowsToPlain(rows); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s fallback rows mismatch\n got: %#v\nwant: %#v", decision.Status, got, want)
		}
		if rows[0].Width() != textWidth(rows[0].Plain()) {
			t.Fatalf("%s row width = %d, plain width = %d", decision.Status, rows[0].Width(), textWidth(rows[0].Plain()))
		}
	}
}

func TestRowsImageCapableModeReservesPlaceholderWidthBeforeCellsReady(t *testing.T) {
	msg := assetHeavyMessage()
	features := config.Default().Features
	features.ImageMode = "normal"
	features.AvatarMode = "image"
	features.EmojiMode = "image"
	features.EmoteMode = "image"
	decision := DecideImageCapabilities(features, DetectTerminalImageSignals([]string{"TERM=xterm-kitty", "KITTY_WINDOW_ID=42", "COLORTERM=truecolor"}), true)
	opts := DefaultOptions(80)
	opts.Assets = decision.AssetOptions()

	rows := Rows(msg, opts)
	want := []string{"[VF] 20:00 [mod] viewer_fan: Kappa  😀"}
	if got := rowsToPlain(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("placeholder fallback rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
	for _, check := range []struct {
		kind FragmentKind
		want int
	}{
		{FragmentAvatar, 5},
		{FragmentBadge, 6},
		{FragmentEmoteFallback, 6},
		{FragmentEmojiFallback, 2},
	} {
		fragment, ok := firstKind(rows, check.kind)
		if !ok {
			t.Fatalf("rows missing %s: %#v", check.kind, rows)
		}
		if got := fragment.Width(); got != check.want {
			t.Fatalf("%s width = %d, want %d: %#v", check.kind, got, check.want, fragment)
		}
		if fragment.ImageReady {
			t.Fatalf("%s image cell is ready before asset render: %#v", check.kind, fragment)
		}
	}
	if rows[0].TerminalString() == "" {
		t.Fatal("terminal string is empty for fallback-only placeholder row")
	}
}

func TestRowsUsePreparedImageCellWithoutChangingFallbackLayout(t *testing.T) {
	msg := assetHeavyMessage()
	features := config.Default().Features
	features.ImageMode = "normal"
	features.EmoteMode = "image"
	decision := DecideImageCapabilities(features, DetectTerminalImageSignals([]string{"TERM=xterm-kitty", "KITTY_WINDOW_ID=42", "COLORTERM=truecolor"}), true)
	asset := storage.AssetRecord{
		Key:         storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:        writeTinyPNG(t),
		MediaType:   "image/png",
		WidthCells:  6,
		HeightCells: 1,
	}
	spec := ImageSpec{
		Ref:         twitch.AssetRef{Kind: "twitch_emote", ID: "25"},
		WidthCells:  6,
		HeightCells: 1,
		Fallback:    "Kappa",
	}
	cell, err := NewKittyRenderer(decision).RenderImage(context.Background(), asset, spec)
	if err != nil {
		t.Fatalf("RenderImage returned error: %v", err)
	}

	opts := DefaultOptions(80)
	opts.Assets = decision.AssetOptions()
	before := Rows(msg, opts)
	key, ok := ImageCellKeyForRef(spec.Ref)
	if !ok {
		t.Fatal("missing image cell key")
	}
	opts.Assets.ImageCells = map[ImageCellKey]ImageCell{key: cell}
	after := Rows(msg, opts)

	if !reflect.DeepEqual(rowsToPlain(after), rowsToPlain(before)) {
		t.Fatalf("fallback rows changed after prepared cell\nbefore: %#v\nafter:  %#v", rowsToPlain(before), rowsToPlain(after))
	}
	if after[0].Width() != before[0].Width() {
		t.Fatalf("row width changed after prepared cell: before=%d after=%d", before[0].Width(), after[0].Width())
	}
	if terminal := after[0].TerminalString(); !strings.Contains(terminal, "\x1b_G") {
		t.Fatalf("terminal row missing prepared Kitty cell: %q", terminal)
	}
	fragment, ok := firstKind(after, FragmentEmoteFallback)
	if !ok || !fragment.ImageReady {
		t.Fatalf("prepared emote fragment = %#v, ok=%v; want image-ready", fragment, ok)
	}
}

func TestRowsRenderFailureCellKeepsFallbackLayout(t *testing.T) {
	msg := assetHeavyMessage()
	features := config.Default().Features
	features.ImageMode = "normal"
	features.EmoteMode = "image"
	decision := DecideImageCapabilities(features, DetectTerminalImageSignals([]string{"TERM=xterm-kitty", "KITTY_WINDOW_ID=42", "COLORTERM=truecolor"}), true)
	spec := ImageSpec{
		Ref:         twitch.AssetRef{Kind: "twitch_emote", ID: "25"},
		WidthCells:  6,
		HeightCells: 1,
		Fallback:    "Kappa",
	}
	cell, err := NewKittyRenderer(decision).RenderImage(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:      "missing.png",
		MediaType: "image/png",
	}, spec)
	if !errors.Is(err, ErrImageRenderFailed) {
		t.Fatalf("RenderImage error = %v, want ErrImageRenderFailed", err)
	}

	opts := DefaultOptions(80)
	opts.Assets = decision.AssetOptions()
	before := Rows(msg, opts)
	key, ok := ImageCellKeyForRef(spec.Ref)
	if !ok {
		t.Fatal("missing image cell key")
	}
	opts.Assets.ImageCells = map[ImageCellKey]ImageCell{key: cell}
	after := Rows(msg, opts)

	if !reflect.DeepEqual(rowsToPlain(after), rowsToPlain(before)) {
		t.Fatalf("fallback rows changed after render failure\nbefore: %#v\nafter:  %#v", rowsToPlain(before), rowsToPlain(after))
	}
	if after[0].Width() != before[0].Width() {
		t.Fatalf("row width changed after render failure: before=%d after=%d", before[0].Width(), after[0].Width())
	}
	if terminal := after[0].TerminalString(); strings.Contains(terminal, "\x1b_G") || !strings.Contains(terminal, "Kappa ") {
		t.Fatalf("terminal row should keep fallback failed cell: %q", terminal)
	}
}

func TestRowsNoImageFallbackOutputIsIntentional(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "twi_bot",
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: "😀"},
		},
	}
	opts := DefaultOptions(80)
	opts.Assets = FallbackAssetOptions()

	rows := Rows(msg, opts)
	want := []string{"[TB] 20:00 twi_bot: Kappa  😀"}
	if got := rowsToPlain(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback rows mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func assetHeavyMessage() twitch.ChatMessage {
	return twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		AuthorID:    "user-1",
		AvatarURL:   "https://static-cdn.example/avatar.png",
		DisplayName: "viewer_fan",
		AuthorLogin: "viewer_fan",
		AuthorColor: "#9146ff",
		Badges:      []twitch.Badge{{SetID: "moderator", ID: "1"}},
		Type:        twitch.MessageTypeChat,
		Fragments: []twitch.MessageFragment{
			{Type: twitch.FragmentEmote, Text: "Kappa", Ref: twitch.AssetRef{Kind: "twitch_emote", ID: "25"}},
			{Type: twitch.FragmentText, Text: " "},
			{Type: twitch.FragmentEmoji, Text: "😀"},
		},
	}
}

func TestRowsMapEmojiAssetKeysAndPreserveGoldenFallback(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "emoji_fan",
		Type:        twitch.MessageTypeChat,
		Text:        "hi ☕️ 👍🏽 👩‍💻 👨‍👩‍👧‍👦 🇺🇸 1️⃣ ♥️",
	}
	opts := DefaultOptions(120)
	opts.Assets.ShowAvatars = false

	rows := Rows(msg, opts)
	golden, err := os.ReadFile("testdata/emoji_rows.golden")
	if err != nil {
		t.Fatalf("read emoji golden: %v", err)
	}
	if got, want := strings.Join(rowsToPlain(rows), "\n")+"\n", string(golden); got != want {
		t.Fatalf("emoji rows mismatch\n got: %q\nwant: %q", got, want)
	}

	wantRefs := []string{
		"2615",
		"1f44d-1f3fd",
		"1f469-200d-1f4bb",
		"1f468-200d-1f469-200d-1f467-200d-1f466",
		"1f1fa-1f1f8",
		"31-20e3",
		"2665",
	}
	var gotRefs []string
	var gotFallbacks []string
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind != FragmentEmojiFallback {
				continue
			}
			gotRefs = append(gotRefs, fragment.Ref.ID)
			gotFallbacks = append(gotFallbacks, fragment.Text)
		}
	}
	if !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Fatalf("emoji refs = %#v, want %#v", gotRefs, wantRefs)
	}
	wantFallbacks := []string{"☕️", "👍🏽", "👩‍💻", "👨‍👩‍👧‍👦", "🇺🇸", "1️⃣", "♥️"}
	if !reflect.DeepEqual(gotFallbacks, wantFallbacks) {
		t.Fatalf("emoji fallbacks = %#v, want %#v", gotFallbacks, wantFallbacks)
	}
}

func TestRowsKeepAdjacentFixedWidthFallbacksSeparate(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		DisplayName: "emoji_fan",
		Type:        twitch.MessageTypeChat,
		Text:        "😀😀",
	}
	opts := DefaultOptions(80)
	opts.Assets = FallbackAssetOptions()
	opts.Assets.ShowAvatars = false

	rows := Rows(msg, opts)
	if got, want := countKind(rows, FragmentEmojiFallback), 2; got != want {
		t.Fatalf("emoji fallback count = %d, want %d: %#v", got, want, rows)
	}
	if got, want := rows[0].Plain(), "20:00 emoji_fan: 😀😀"; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind == FragmentEmojiFallback && fragment.Width() != 2 {
				t.Fatalf("emoji width = %d, want 2: %#v", fragment.Width(), fragment)
			}
		}
	}
}

func TestTextRowUsesFallbackAuthor(t *testing.T) {
	msg := twitch.ChatMessage{
		Timestamp:   time.Date(2026, 7, 1, 20, 0, 0, 0, time.Local),
		AuthorLogin: "login",
		Text:        "message",
	}

	row := TextRow(msg, 80)

	if !strings.Contains(row, "login") {
		t.Fatalf("row = %q, want fallback author login", row)
	}
}

func plainRows(msg twitch.ChatMessage, width int) []string {
	rows := Rows(msg, DefaultOptions(width))
	return rowsToPlain(rows)
}

func rowsToPlain(rows []Row) []string {
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain())
	}
	return plain
}

func hasKind(rows []Row, kind FragmentKind) bool {
	_, ok := firstKind(rows, kind)
	return ok
}

func firstKind(rows []Row, kind FragmentKind) (Fragment, bool) {
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind == kind {
				return fragment, true
			}
		}
	}
	return Fragment{}, false
}

func countKind(rows []Row, kind FragmentKind) int {
	count := 0
	for _, row := range rows {
		for _, fragment := range row.Fragments {
			if fragment.Kind == kind {
				count++
			}
		}
	}
	return count
}

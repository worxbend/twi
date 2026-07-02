package render

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/w0rxbend/twi/internal/theme"
	"github.com/w0rxbend/twi/internal/twitch"
)

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
		if got := textWidth(fragmentDisplayText(fragment)); got != check.want {
			t.Fatalf("%s fallback display width = %d, want %d: %q", check.kind, got, check.want, fragmentDisplayText(fragment))
		}
		if fragment.Ref != check.ref {
			t.Fatalf("%s ref = %#v, want %#v", check.kind, fragment.Ref, check.ref)
		}
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

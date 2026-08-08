package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/worxbend/twi/internal/config"
)

func splashTestModel(t *testing.T, width, height int) (shellModel, time.Time) {
	t.Helper()
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	model.width, model.height = width, height
	start := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	model.splashUntil = start.Add(splashDuration)
	return model, start
}

func splashAt(model shellModel, start time.Time, percent int) string {
	return ansi.Strip(model.splashViewAt(start.Add(splashDuration * time.Duration(percent) / 100)))
}

func TestSplashRunsForTenSeconds(t *testing.T) {
	if splashDuration != 10*time.Second {
		t.Fatalf("splashDuration = %v, want 10s", splashDuration)
	}
}

// TestSplashChatRevealsMessagesOverTime is the animation itself: the chat has
// to build up rather than appear all at once, or it is a static picture.
func TestSplashChatRevealsMessagesOverTime(t *testing.T) {
	model, start := splashTestModel(t, 80, 24)

	counts := make([]int, 0, 5)
	for _, percent := range []int{5, 25, 45, 65, 95} {
		view := splashAt(model, start, percent)
		seen := 0
		for _, line := range splashChatScript {
			// Compare on a prefix so a half-typed line still counts.
			head := line.text
			if len(head) > 8 {
				head = head[:8]
			}
			if strings.Contains(view, head) {
				seen++
			}
		}
		counts = append(counts, seen)
	}

	if counts[0] != 0 {
		t.Errorf("chat already showing %d messages at 5%%, want none while the logo lands", counts[0])
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] < counts[i-1] {
			t.Fatalf("message count went backwards: %v", counts)
		}
	}
	if counts[len(counts)-1] == 0 {
		t.Fatalf("no chat messages by the end of the splash: %v", counts)
	}
	if counts[len(counts)-1] <= counts[1] {
		t.Fatalf("chat did not grow between 25%% and 95%%: %v", counts)
	}
}

// TestSplashChatTypesTheNewestLine covers the per-message reveal, which is
// what makes the sequence read as chat arriving rather than a slideshow.
func TestSplashChatTypesTheNewestLine(t *testing.T) {
	rows := splashChatRowsPlain(t, 0.20)
	if len(rows) == 0 {
		t.Fatal("no chat rows mid-splash")
	}
	if !strings.Contains(rows[len(rows)-1], "▍") {
		t.Fatalf("newest row has no typing caret: %q", rows[len(rows)-1])
	}
	for i, row := range rows[:len(rows)-1] {
		if strings.Contains(row, "▍") {
			t.Fatalf("settled row %d still shows a caret: %q", i, row)
		}
	}
}

// TestSplashChatScrollsToTheNewest keeps a script longer than the available
// rows from either overflowing or freezing on the first messages.
func TestSplashChatScrollsToTheNewest(t *testing.T) {
	model, start := splashTestModel(t, 80, 24)
	view := splashAt(model, start, 97)

	last := splashChatScript[len(splashChatScript)-1].text
	if !strings.Contains(view, last) {
		t.Fatalf("final splash frame does not show the newest message %q", last)
	}
	if len(splashChatScript) > splashChatMaxRows {
		first := splashChatScript[0].text
		if strings.Contains(view, first) {
			t.Fatalf("oldest message %q is still on screen; the chat did not scroll", first)
		}
	}
}

func TestSplashChatRowsAreExactlyContentWidth(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	for _, width := range []int{24, 40, 54, 72} {
		for _, fraction := range []float64{0.2, 0.5, 0.8, 1.0} {
			for i, row := range model.splashChatRows(fraction, width, splashChatMaxRows) {
				if got := lipgloss.Width(row); got != width {
					t.Fatalf("width=%d fraction=%.1f row %d is %d cells, want %d: %q",
						width, fraction, i, got, width, ansi.Strip(row))
				}
			}
		}
	}
}

// TestSplashChatIsOmittedWhenThereIsNoRoom keeps the chat from pushing the
// logo or the progress bar off a short terminal.
func TestSplashChatIsOmittedWhenThereIsNoRoom(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4, 5, 8} {
		model, start := splashTestModel(t, 80, height)
		view := model.splashViewAt(start.Add(splashDuration / 2))
		if lines := strings.Split(view, "\n"); len(lines) > height {
			t.Fatalf("height=%d rendered %d lines, want at most %d", height, len(lines), height)
		}
	}
}

func TestSplashChatIsOmittedOnNarrowTerminals(t *testing.T) {
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	if rows := model.splashChatRows(0.5, 12, splashChatMaxRows); len(rows) != 0 {
		t.Fatalf("rendered %d chat rows at 12 columns, want none", len(rows))
	}
}

// TestSplashChatIsDeterministic matters because every splash frame is a pure
// function of elapsed time: two renders of the same instant must agree, or
// the animation would flicker between frames.
func TestSplashChatIsDeterministic(t *testing.T) {
	model, start := splashTestModel(t, 80, 24)
	at := start.Add(splashDuration * 6 / 10)
	first := model.splashViewAt(at)
	second := model.splashViewAt(at)
	if first != second {
		t.Fatal("two renders of the same instant differ")
	}
	// And a different instant must actually differ, or "deterministic" would
	// be satisfied by a splash that never animates at all.
	if model.splashViewAt(start.Add(splashDuration*8/10)) == first {
		t.Fatal("splash frames are identical across different instants")
	}
}

// TestSplashIsSkippable is the counterweight to a ten-second splash: nobody
// should be stuck watching it.
func TestSplashIsSkippable(t *testing.T) {
	model, _ := splashTestModel(t, 80, 24)
	// splashActive consults the wall clock, so the deadline has to be ahead
	// of real now rather than the fixed instant the render tests use.
	model.splashUntil = time.Now().Add(splashDuration)
	if !model.splashActive() {
		t.Fatal("test setup: splash is not active")
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if updated.(shellModel).splashActive() {
		t.Fatal("splash survived a keypress; a ten-second wait must be escapable")
	}
}

// TestSplashAdvertisesTheSkip: a skippable wait nobody knows is skippable is
// just a wait.
func TestSplashAdvertisesTheSkip(t *testing.T) {
	model, start := splashTestModel(t, 88, 24)
	if view := splashAt(model, start, 40); !strings.Contains(view, "press any key to skip") {
		t.Fatalf("splash does not mention the skip:\n%s", view)
	}
}

// TestSplashDisabledWhenAnimationIsOff keeps the splash out of the way for
// anyone who has already asked for no motion.
func TestSplashDisabledWhenAnimationIsOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.AnimationMode = "off"
	model := newMockModel("alpha", cfg)
	if model.splashActive() {
		t.Fatal("splash is active with animation_mode=off")
	}
}

func TestSplashChatProgressBounds(t *testing.T) {
	for _, tt := range []struct {
		fraction     float64
		wantRevealed int
	}{
		{0, 0},
		{splashChatStart - 0.01, 0},
		{1, len(splashChatScript)},
		{1.5, len(splashChatScript)},
	} {
		revealed, typing := splashChatProgress(tt.fraction)
		if revealed != tt.wantRevealed {
			t.Errorf("fraction=%.2f revealed = %d, want %d", tt.fraction, revealed, tt.wantRevealed)
		}
		if typing < 0 || typing > 1 {
			t.Errorf("fraction=%.2f typing = %f, want it within [0,1]", tt.fraction, typing)
		}
	}
}

func TestTruncateToFractionKeepsGraphemesWhole(t *testing.T) {
	const text = "hey chat 👋 ʕ•ᴥ•ʔ"
	for i := range 21 {
		got := truncateToFraction(text, float64(i)/20)
		if !strings.HasPrefix(text, got) {
			t.Fatalf("truncateToFraction(%.2f) = %q, which is not a prefix of the original", float64(i)/20, got)
		}
	}
	if got := truncateToFraction(text, 1); got != text {
		t.Fatalf("full fraction = %q, want the whole string", got)
	}
}

func splashChatRowsPlain(t *testing.T, fraction float64) []string {
	t.Helper()
	forceColorProfile(t)
	model := newMockModel("alpha", config.Default())
	rows := model.splashChatRows(fraction, 54, splashChatMaxRows)
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, ansi.Strip(row))
	}
	return plain
}

package app

import (
	"reflect"
	"testing"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
)

// TestLiveModelReadsEveryFeatureSetting is the regression guard for a class of
// bug rather than one instance of it.
//
// The mock and live constructors were written and maintained separately, and
// the live one silently stopped reading four settings: message_layout,
// badge_mode, highlight_emotes and full_username were parsed by config,
// validated by doctor, and written back to disk by the display toggles, while
// doing nothing at all in the only mode that connects to Twitch. Nothing
// failed; the settings were simply ignored.
//
// This sets every FeatureConfig field to a non-default value and asserts the
// live model reflects it, so the next setting wired into one path and not the
// other fails here instead of shipping.
func TestLiveModelReadsEveryFeatureSetting(t *testing.T) {
	cfg := config.Default()
	cfg.Features.MessageLayout = "grouped"
	cfg.Features.BadgeMode = "text"
	cfg.Features.HighlightEmotes = false
	cfg.Features.FullUsername = true
	cfg.Features.EnableMouse = false
	cfg.Features.AvatarMode = "off"
	cfg.Features.AnimationMode = "off"
	cfg.Features.ScrollbackLimit = 321

	live := newLiveModelWithClock("example", cfg, nil, nil)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"MessageLayout", live.messageLayout, render.NormalizeLayoutMode("grouped")},
		{"BadgeMode", live.badgeMode, render.NormalizeBadgeMode("text")},
		{"HighlightEmotes", live.highlightEmotes, false},
		{"FullUsername", live.fullUsername, true},
		{"EnableMouse", live.mouseEnabled, false},
		{"AvatarMode", live.avatarMode, "off"},
		{"ScrollbackLimit", live.channels.scrollbackLimit, 321},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("live model ignored cfg.Features.%s: got %v, want %v", c.field, c.got, c.want)
		}
	}
}

// TestFeatureConfigFieldsAreAllCovered fails when a field is added to
// FeatureConfig without a corresponding assertion above, so the guard cannot
// quietly fall behind the struct it is guarding.
func TestFeatureConfigFieldsAreAllCovered(t *testing.T) {
	covered := map[string]bool{
		"MessageLayout":   true,
		"BadgeMode":       true,
		"HighlightEmotes": true,
		"FullUsername":    true,
		"EnableMouse":     true,
		"AvatarMode":      true,
		"ScrollbackLimit": true,
		// Resolved into the model's palette rather than stored as a name.
		"ThemeName":   true,
		"ThemeCustom": true,
		// Consumed by the animation config, asserted via animationMode.
		"AnimationMode": true,
		// Read at their point of use, not at construction.
		"StreamStatusMode":      true,
		"EmoteAutocompleteMode": true,
	}

	features := reflect.TypeOf(config.FeatureConfig{})
	for i := range features.NumField() {
		name := features.Field(i).Name
		if !covered[name] {
			t.Errorf("config.FeatureConfig.%s is not covered by TestLiveModelReadsEveryFeatureSetting; "+
				"add an assertion there (or mark it here) so live/mock drift stays caught", name)
		}
	}
}

// TestMockAndLiveModelsAgreeOnConfigDerivedState pins the two constructors to
// each other for everything that comes from configuration alone.
func TestMockAndLiveModelsAgreeOnConfigDerivedState(t *testing.T) {
	cfg := config.Default()
	cfg.Features.MessageLayout = "compact"
	cfg.Features.BadgeMode = "off"
	cfg.Features.HighlightEmotes = false
	cfg.Features.FullUsername = true
	cfg.Features.AnimationMode = "off"

	mock := newMockModelWithClock("example", cfg, nil)
	live := newLiveModelWithClock("example", cfg, nil, nil)

	if mock.messageLayout != live.messageLayout {
		t.Errorf("messageLayout: mock %v, live %v", mock.messageLayout, live.messageLayout)
	}
	if mock.badgeMode != live.badgeMode {
		t.Errorf("badgeMode: mock %v, live %v", mock.badgeMode, live.badgeMode)
	}
	if mock.highlightEmotes != live.highlightEmotes {
		t.Errorf("highlightEmotes: mock %v, live %v", mock.highlightEmotes, live.highlightEmotes)
	}
	if mock.fullUsername != live.fullUsername {
		t.Errorf("fullUsername: mock %v, live %v", mock.fullUsername, live.fullUsername)
	}
	if mock.membershipBurstIndex != live.membershipBurstIndex {
		t.Errorf("membershipBurstIndex: mock %d, live %d; -1 means no burst is open",
			mock.membershipBurstIndex, live.membershipBurstIndex)
	}
	if mock.animationMode != live.animationMode {
		t.Errorf("animationMode: mock %q, live %q", mock.animationMode, live.animationMode)
	}
}

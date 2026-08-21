package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSettingsTableIsWellFormed guards the invariants the settings table is
// relied on for: unique names, an apply function for every setting, and a
// format function for exactly the settings written back to a config file.
func TestSettingsTableIsWellFormed(t *testing.T) {
	seenKey := map[string]bool{}
	seenEnv := map[string]bool{}
	for _, s := range settings {
		if s.key == "" || s.env == "" {
			t.Errorf("setting %+v has an empty key or env name", s)
			continue
		}
		if seenKey[s.key] {
			t.Errorf("duplicate config key %q", s.key)
		}
		if seenEnv[s.env] {
			t.Errorf("duplicate environment variable %q", s.env)
		}
		seenKey[s.key] = true
		seenEnv[s.env] = true

		if s.apply == nil {
			t.Errorf("setting %q has no apply function, so nothing can set it", s.key)
		}
		if s.persisted && s.format == nil {
			t.Errorf("setting %q is persisted but has no format function", s.key)
		}
	}
}

// TestSettingsEnvNamesFollowConvention documents that the environment
// variable for a setting is its key uppercased behind a TWI_ prefix, and
// names the one setting that predates the convention.
func TestSettingsEnvNamesFollowConvention(t *testing.T) {
	exceptions := map[string]string{"debug_logging": "TWI_DEBUG_LOG"}
	for _, s := range settings {
		want, ok := exceptions[s.key]
		if !ok {
			want = "TWI_" + strings.ToUpper(s.key)
		}
		if s.env != want {
			t.Errorf("setting %q has env %q, want %q", s.key, s.env, want)
		}
	}
}

// TestEverySettingRoundTripsThroughAFile writes a config in which every
// persisted setting has a non-default value, reads it back, and checks the
// value survived. It fails if a setting is written but not parsed, or parsed
// but not written -- the drift the single table exists to prevent.
func TestEverySettingRoundTripsThroughAFile(t *testing.T) {
	for _, s := range settings {
		if !s.persisted {
			continue
		}
		t.Run(s.key, func(t *testing.T) {
			var cfg Config
			s.apply(&cfg, sampleValueFor(s.key))
			want := s.format(cfg)

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(s.key+" = "+want+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var reloaded Config
			if err := applyFile(&reloaded, path); err != nil {
				t.Fatal(err)
			}
			if got := s.format(reloaded); got != want {
				t.Errorf("%s round-tripped as %s, want %s", s.key, got, want)
			}
		})
	}
}

// TestEverySettingReadsItsEnvironmentVariable checks each setting's
// environment variable actually reaches its field.
func TestEverySettingReadsItsEnvironmentVariable(t *testing.T) {
	for _, s := range settings {
		if s.format == nil {
			continue
		}
		t.Run(s.env, func(t *testing.T) {
			value := sampleValueFor(s.key)

			var want Config
			s.apply(&want, value)

			var got Config
			applyEnv(&got, []string{s.env + "=" + value})

			if s.format(got) != s.format(want) {
				t.Errorf("%s set the field to %s, want %s", s.env, s.format(got), s.format(want))
			}
		})
	}
}

// sampleValueFor returns a non-default value suitable for the setting's type.
func sampleValueFor(key string) string {
	switch key {
	case "enable_mouse", "highlight_emotes", "full_username", "debug_logging":
		return "true"
	case "sidebar_width", "activity_width", "scrollback_limit":
		return "37"
	case "default_channels":
		return "alpha,beta"
	default:
		return "sample-" + key
	}
}

// TestRedactedStringReportsEverySetting is the regression test for `twi
// config show` quietly omitting settings. It used to be missing seven of
// them, because its list of settings was maintained by hand separately from
// the lists used to read and write them.
func TestRedactedStringReportsEverySetting(t *testing.T) {
	output := Config{}.RedactedString()
	for _, s := range settings {
		if !strings.Contains(output, "\n"+s.key+" = ") {
			t.Errorf("`twi config show` does not report %q", s.key)
		}
	}
}

// TestRedactedStringRedactsEverySecret checks that a setting with no format
// function -- the marker for a credential -- never has its value printed.
func TestRedactedStringRedactsEverySecret(t *testing.T) {
	const secret = "s3cr3t-value-that-must-not-appear"
	for _, s := range settings {
		if s.format != nil {
			continue
		}
		t.Run(s.key, func(t *testing.T) {
			var cfg Config
			s.apply(&cfg, secret)
			if output := cfg.RedactedString(); strings.Contains(output, secret) {
				t.Errorf("`twi config show` leaked %s:\n%s", s.key, output)
			}
		})
	}
}

package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
	"io"
	"strings"
	"time"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type csvChannels struct{ channels *stringList }

func (c csvChannels) String() string {
	if c.channels == nil {
		return ""
	}
	return c.channels.String()
}
func (c csvChannels) Set(value string) error {
	*c.channels = append(*c.channels, strings.Split(value, ",")...)
	return nil
}

type dataOptions struct {
	account                                           string
	channels                                          stringList
	configPath, format, diagnosticFormat, historyPath string
	quiet, json                                       bool
	connectTimeout                                    time.Duration
}

func addDataFlags(fs *flag.FlagSet, o *dataOptions, history bool) {
	fs.Var(&o.channels, "channel", "channel login; repeatable")
	fs.Var(&o.channels, "t", "channel login; repeatable")
	fs.Var(csvChannels{&o.channels}, "channels", "comma-separated channels")
	fs.StringVar(&o.configPath, "config", "", "config path")
	fs.StringVar(&o.account, "account", "", "explicit named credential account")
	fs.StringVar(&o.format, "format", "jsonl", "jsonl or text")
	fs.StringVar(&o.diagnosticFormat, "diagnostic-format", "text", "text or jsonl diagnostics")
	fs.BoolVar(&o.json, "J", false, "JSONL output")
	fs.BoolVar(&o.quiet, "quiet", false, "suppress informational diagnostics")
	fs.BoolVar(&o.quiet, "q", false, "suppress informational diagnostics")
	fs.DurationVar(&o.connectTimeout, "connect-timeout", 30*time.Second, "initial readiness timeout")
	if history {
		fs.StringVar(&o.historyPath, "history", "", "history database path")
	}
}
func validateDataOptions(o *dataOptions) error {
	if o.account != "" {
		if err := storage.ValidateAccountName(o.account); err != nil {
			return err
		}
	}
	channels, err := twitch.ParseChannels([]string(o.channels))
	if err != nil {
		return err
	}
	o.channels = make(stringList, 0, len(channels))
	for _, channel := range channels {
		o.channels = append(o.channels, channel.String())
	}
	if o.format != "jsonl" && o.format != "text" {
		return fmt.Errorf("format must be jsonl or text")
	}
	if o.json && o.format == "text" {
		return fmt.Errorf("-J conflicts with text output")
	}
	if o.diagnosticFormat != "jsonl" && o.diagnosticFormat != "text" {
		return fmt.Errorf("diagnostic-format must be jsonl or text")
	}
	if o.connectTimeout <= 0 {
		return fmt.Errorf("connect-timeout must be positive")
	}
	return nil
}
func dataDiagnostic(w io.Writer, o dataOptions, code string, err error) {
	if errors.Is(err, history.ErrStorageFull) {
		code = "storage_full"
	}
	message := config.RedactDisplayValue(err.Error())
	if o.diagnosticFormat == "jsonl" {
		_ = json.NewEncoder(w).Encode(struct {
			SchemaVersion int    `json:"schema_version"`
			Type          string `json:"type"`
			Code          string `json:"code"`
			Message       string `json:"message"`
		}{1, "diagnostic", code, message})
	} else {
		_, _ = fmt.Fprintf(w, "%s: %s\n", code, message)
	}
}
func parseDataTime(value string, start time.Time) (time.Time, error) {
	if value == "now" {
		return start, nil
	}
	if d, e := time.ParseDuration(value); e == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("relative time must be positive")
		}
		return start.Add(-d), nil
	}
	t, e := time.Parse(time.RFC3339Nano, value)
	if e != nil {
		return t, fmt.Errorf("time must be RFC3339, now, or a positive duration")
	}
	return t, nil
}

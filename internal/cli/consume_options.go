package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/worxbend/twi/internal/twitch/eventsub"
	"io"
	"strconv"
	"time"
)

type consumeOptions struct {
	dataOptions
	source, offset, since, until, timeField, after, cursorFile, events, transport string
	last, count                                                                   int
	follow, exit, allowPartial, textOnly                                          bool
	duration, idle                                                                time.Duration
	historical                                                                    bool
}

func parseConsume(args []string, w io.Writer) (consumeOptions, int, error) {
	o := consumeOptions{}
	fs := flag.NewFlagSet("consume", flag.ContinueOnError)
	fs.SetOutput(w)
	addDataFlags(fs, &o.dataOptions, true)
	fs.StringVar(&o.source, "source", "auto", "auto, live, or history")
	fs.StringVar(&o.offset, "offset", "", "beginning, end, or -N")
	fs.StringVar(&o.offset, "o", "", "beginning, end, or -N")
	fs.IntVar(&o.last, "last", 0, "last N messages per channel")
	fs.StringVar(&o.since, "since", "", "inclusive RFC3339 or relative time")
	fs.StringVar(&o.until, "until", "", "exclusive upper time bound")
	fs.StringVar(&o.timeField, "time-field", "event", "event or received")
	fs.StringVar(&o.after, "after", "", "opaque history cursor")
	fs.StringVar(&o.cursorFile, "cursor-file", "", "resume/checkpoint file")
	fs.BoolVar(&o.follow, "follow", false, "follow recorded messages")
	fs.BoolVar(&o.follow, "f", false, "follow recorded messages")
	fs.BoolVar(&o.exit, "exit", false, "exit at snapshot EOF")
	fs.BoolVar(&o.exit, "e", false, "exit at snapshot EOF")
	fs.IntVar(&o.count, "count", 0, "maximum total emitted records")
	fs.IntVar(&o.count, "c", 0, "maximum total emitted records")
	fs.DurationVar(&o.duration, "duration", 0, "maximum command lifetime")
	fs.DurationVar(&o.idle, "idle-timeout", 0, "stop after no emitted records")
	fs.BoolVar(&o.allowPartial, "allow-partial", false, "accept known capture gaps")
	fs.StringVar(&o.events, "events", "messages", "messages or all")
	fs.BoolVar(&o.textOnly, "text-only", false, "text bodies only")
	fs.StringVar(&o.transport, "transport", "irc", "irc or eventsub")
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return o, 0, e
		}
		return o, 2, e
	}
	fail := func(s string) (consumeOptions, int, error) { return o, 2, fmt.Errorf("%s", s) }
	if e := validateDataOptions(&o.dataOptions); e != nil {
		return o, 2, e
	}
	if fs.NArg() != 0 {
		return fail("unexpected positional arguments")
	}
	if len(o.channels) == 0 {
		return fail("explicit channels required")
	}
	selected := 0
	for _, s := range []string{o.offset, o.since, o.after} {
		if s != "" {
			selected++
		}
	}
	if o.last != 0 {
		selected++
	}
	if selected > 1 {
		return fail("start selectors are mutually exclusive")
	}
	if o.last < 0 || o.count < 0 || o.duration < 0 || o.idle < 0 {
		return fail("counts and durations must be positive")
	}
	var invalidPositive bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "last", "count", "c":
			v, _ := strconv.Atoi(f.Value.String())
			invalidPositive = invalidPositive || v <= 0
		case "duration", "idle-timeout":
			v, _ := time.ParseDuration(f.Value.String())
			invalidPositive = invalidPositive || v <= 0
		}
	})
	if invalidPositive {
		return fail("explicit counts and durations must be positive")
	}
	if o.offset != "" && o.offset != "beginning" && o.offset != "end" {
		n, e := strconv.Atoi(o.offset)
		if e != nil || n >= 0 {
			return fail("offset must be beginning, end, or -N")
		}
		if -n <= 0 {
			return fail("negative offset is out of range")
		}
		o.last = -n
	}
	if o.source != "auto" && o.source != "live" && o.source != "history" {
		return fail("invalid source")
	}
	o.historical = selected > 0 || o.cursorFile != "" || o.source == "history"
	if o.source == "live" && o.historical {
		return fail("live source rejects historical selectors")
	}
	if o.source == "history" && selected == 0 && o.cursorFile == "" {
		return fail("history requires an initial selector or cursor file")
	}
	if o.until != "" && (o.since == "" && o.offset != "beginning" || o.last > 0 || o.after != "" || o.cursorFile != "") {
		return fail("until requires since or beginning")
	}
	if o.after != "" && o.cursorFile != "" {
		return fail("after conflicts with cursor-file")
	}
	if o.follow && o.exit {
		return fail("follow conflicts with exit")
	}
	if o.exit && !o.historical {
		return fail("exit requires historical selection")
	}
	if !o.historical && o.historyPath != "" {
		return fail("history path requires historical selection")
	}
	if o.timeField != "event" && o.timeField != "received" {
		return fail("time-field must be event or received")
	}
	if o.events != "messages" && o.events != "all" {
		return fail("events must be messages or all")
	}
	if o.textOnly && (o.format != "text" || o.events != "messages") {
		return fail("text-only requires text message output")
	}
	if o.transport != "irc" && o.transport != "eventsub" {
		return fail("invalid transport")
	}
	if !o.historical && (len(o.channels) > 100 || (o.transport == "eventsub" && len(o.channels) > eventsub.MaxReadChannels)) {
		return fail("too many selected channels for reader transport")
	}
	return o, 0, nil
}

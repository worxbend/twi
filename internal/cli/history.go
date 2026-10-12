package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/worxbend/twi/internal/history"
	"io"
	"time"
)

func runHistory(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	stdout, stderr = dataStreams(ctx, stdout, stderr)
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: twi history status|prune|search|upgrade [--history PATH]")
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "usage: twi history status|prune|search|upgrade [--history PATH]")
		return 0
	}
	if args[0] == "search" {
		return runHistorySearch(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "status" && args[0] != "prune" && args[0] != "upgrade" {
		fmt.Fprintln(stderr, "unknown history command")
		return 2
	}
	o := dataOptions{format: "jsonl", diagnosticFormat: "text", connectTimeout: 30 * time.Second}
	fs := flag.NewFlagSet("history "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.historyPath, "history", "", "history database path")
	fs.StringVar(&o.format, "format", "jsonl", "jsonl or text")
	fs.StringVar(&o.diagnosticFormat, "diagnostic-format", "text", "text or jsonl")
	age := 168 * time.Hour
	budget := "1GiB"
	dry := false
	if args[0] == "prune" {
		fs.DurationVar(&age, "retention", age, "retained age")
		fs.StringVar(&budget, "max-bytes", budget, "target store budget")
		fs.BoolVar(&dry, "dry-run", false, "preview pruning")
	}
	fs.Usage = func() {
		fmt.Fprintf(stdout, "Usage: twi history %s [--history PATH]\n", args[0])
		fs.SetOutput(stdout)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	maxBytes, err := parseByteBudget(budget)
	if err != nil || age <= 0 || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "invalid retention arguments")
		return 2
	}
	if err = validateDataOptions(&o); err != nil {
		dataDiagnostic(stderr, o, "invalid_arguments", err)
		return 2
	}
	var store *history.Store
	// Maintenance requires an existing database. Upgrade never changes retention
	// policy or starts network recording; dry-run pruning remains read-only.
	if args[0] == "upgrade" || args[0] == "prune" && !dry {
		store, err = history.OpenExistingWriterContext(ctx, o.historyPath)
	} else {
		store, err = history.Open(o.historyPath)
	}
	if err != nil {
		dataDiagnostic(stderr, o, "history_unavailable", err)
		return historyCode(err)
	}
	defer store.Close()
	if args[0] == "status" || args[0] == "upgrade" {
		result, e := store.Status(ctx)
		err = e
		if err == nil {
			if o.format == "jsonl" {
				err = encodeHistoryJSON(stdout, result)
			} else {
				_, err = fmt.Fprintf(stdout, "store %s sequence %d retained after %d captures %d gaps %d\n", result.UUID, result.HighWater, result.LowWater, len(result.Captures), len(result.Gaps))
			}
		}
	} else {
		result, e := store.Prune(ctx, history.Retention{Age: age, MaxBytes: maxBytes, Now: time.Now().UTC()}, dry)
		err = e
		if err == nil {
			if o.format == "jsonl" {
				err = encodeHistoryJSON(stdout, result)
			} else {
				_, err = fmt.Fprintf(stdout, "records %d through %d bytes %d dry-run %t\n", result.Records, result.Through, result.Bytes, result.DryRun)
			}
		}
	}
	if err != nil {
		dataDiagnostic(stderr, o, "storage_failed", err)
		return 7
	}
	return 0
}

type historyCheckedWriter struct{ io.Writer }

func (w historyCheckedWriter) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	if n < len(p) && e == nil {
		e = io.ErrShortWrite
	}
	return n, e
}
func encodeHistoryJSON(w io.Writer, v any) error {
	return json.NewEncoder(historyCheckedWriter{w}).Encode(v)
}

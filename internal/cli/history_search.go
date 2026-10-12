package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"syscall"

	"github.com/worxbend/twi/internal/history"
	"github.com/worxbend/twi/internal/output"
)

func runHistorySearch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var o dataOptions
	fs := flag.NewFlagSet("history search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addDataFlags(fs, &o, true)
	phrase := fs.String("query", "", "literal phrase to search in captured messages")
	limit := fs.Int("limit", 100, "maximum results, 1 to 1000")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := validateDataOptions(&o); err != nil {
		dataDiagnostic(stderr, o, "invalid_arguments", err)
		return 2
	}
	if fs.NArg() != 0 || *phrase == "" || len(*phrase) > 4096 || *limit < 1 || *limit > 1000 {
		dataDiagnostic(stderr, o, "invalid_arguments", errors.New("--query and limit between 1 and 1000 required"))
		return 2
	}
	store, err := history.Open(o.historyPath)
	if err != nil {
		dataDiagnostic(stderr, o, "storage_failed", err)
		return 7
	}
	defer store.Close()
	records, err := store.Search(ctx, *phrase, o.channels, *limit)
	if err != nil {
		dataDiagnostic(stderr, o, "storage_failed", err)
		return 7
	}
	for _, r := range records {
		r, visible, err := store.Refresh(ctx, r, false)
		if err != nil {
			dataDiagnostic(stderr, o, "storage_failed", err)
			return 7
		}
		if visible {
			if err := output.Write(stdout, r, o.format, false); err != nil {
				if errors.Is(err, syscall.EPIPE) || ctx.Err() != nil {
					return 0
				}
				dataDiagnostic(stderr, o, "output_failed", err)
				return 7
			}
		}
	}
	return 0
}

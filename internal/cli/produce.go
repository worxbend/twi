package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/worxbend/twi/internal/automation"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/producer"
	"github.com/worxbend/twi/internal/textsafe"
	"github.com/worxbend/twi/internal/twitch"
	"golang.org/x/term"
)

const maxProduceRecord = 64 * 1024

type producerOptions struct {
	transport, authMode, senderLogin, sharedChat string
	action                                       bool
	dataOptions
	messages, files                              stringList
	inputFormat, delimiter, reply, onError, rate string
	skipEmpty, broadcast, dryRun                 bool
	interval, timeout, duration                  time.Duration
}
type producerInput struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	Reply    string `json:"reply_to_message_id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Action   bool   `json:"action,omitempty"`
}

func parseProducer(args []string, stdout, stderr io.Writer) (producerOptions, int, bool) {
	var o producerOptions
	fs := flag.NewFlagSet("produce", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addDataFlags(fs, &o.dataOptions, false)
	fs.StringVar(&o.transport, "transport", "helix", "send transport: helix or explicit unconfirmed irc")
	fs.StringVar(&o.authMode, "auth", "user", "authorization: user or app")
	fs.StringVar(&o.senderLogin, "sender", "", "explicit app-token sender login")
	fs.StringVar(&o.sharedChat, "shared-chat", "", "app-token delivery: source or all (default source)")
	fs.BoolVar(&o.action, "action", false, "IRC action message; requires --transport irc")
	fs.Var(&o.messages, "message", "explicit message; repeatable")
	fs.Var(&o.messages, "m", "explicit message; repeatable")
	fs.Var(&o.files, "file", "input file; repeatable; - for stdin")
	fs.StringVar(&o.inputFormat, "input-format", "lines", "lines or jsonl")
	fs.StringVar(&o.delimiter, "delimiter", "newline", "newline or nul")
	fs.StringVar(&o.reply, "reply-to", "", "reply parent ID")
	fs.StringVar(&o.onError, "on-error", "stop", "stop or continue")
	fs.StringVar(&o.rate, "rate-limit", "wait", "wait or fail")
	fs.BoolVar(&o.skipEmpty, "skip-empty", false, "skip empty lines")
	fs.BoolVar(&o.broadcast, "broadcast", false, "explicit multi-channel fan-out")
	fs.BoolVar(&o.dryRun, "dry-run", false, "validate without authentication or sending")
	fs.DurationVar(&o.interval, "min-interval", 0, "additional per-channel send interval")
	fs.DurationVar(&o.timeout, "request-timeout", 15*time.Second, "per-send request timeout")
	fs.DurationVar(&o.duration, "duration", 0, "maximum total producer duration")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: twi produce -t CHANNEL [-m TEXT | --file PATH] [--dry-run]")
		fs.PrintDefaults()
	}

	var parseOutput bytes.Buffer
	fs.SetOutput(&parseOutput)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(stdout)
			fs.Usage()
			return o, 0, false
		}
		_, _ = io.Copy(stderr, &parseOutput)
		return o, 2, false
	}
	fs.SetOutput(stderr)
	invalid := func(message string) (producerOptions, int, bool) {
		dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", errors.New(message))
		return o, 2, false
	}
	if fs.NArg() != 0 {
		return invalid("stray positional arguments")
	}
	if err := validateDataOptions(&o.dataOptions); err != nil {
		return invalid(err.Error())
	}
	if o.transport != "helix" && o.transport != "irc" || o.authMode != "user" && o.authMode != "app" {
		return invalid("transport must be helix or irc; auth must be user or app")
	}
	if o.authMode == "app" {
		if o.transport != "helix" || o.senderLogin == "" {
			return invalid("app sending requires Helix and explicit --sender")
		}
		if o.sharedChat == "" {
			o.sharedChat = "source"
		}
		if o.sharedChat != "source" && o.sharedChat != "all" {
			return invalid("shared-chat must be source or all")
		}
		if channel, err := twitch.ParseChannel(o.senderLogin); err != nil {
			return invalid("invalid app sender login")
		} else {
			o.senderLogin = channel.String()
		}
	} else if o.senderLogin != "" || o.sharedChat != "" {
		return invalid("sender/shared-chat options require --auth app")
	}
	if o.action && (o.transport != "irc" || o.inputFormat == "jsonl") {
		return invalid("--action requires IRC lines input; JSONL uses per-record action")
	}
	if len(o.messages) > 0 && len(o.files) > 0 {
		return invalid("--message and --file are mutually exclusive")
	}
	if o.inputFormat != "lines" && o.inputFormat != "jsonl" || o.delimiter != "newline" && o.delimiter != "nul" || o.onError != "stop" && o.onError != "continue" || o.rate != "wait" && o.rate != "fail" {
		return invalid("unsupported producer option")
	}
	if o.timeout <= 0 || o.interval < 0 || o.duration < 0 {
		return invalid("invalid producer duration")
	}
	if o.inputFormat == "jsonl" && (o.broadcast || o.reply != "" || o.delimiter != "newline" || len(o.messages) > 0) {
		return invalid("JSONL conflicts with broadcast, reply-to, delimiter, or message")
	}
	if o.inputFormat == "lines" && (len(o.channels) == 0 || len(o.channels) > 1 && !o.broadcast) {
		return invalid("lines require one channel or explicit --broadcast")
	}
	if o.reply != "" && len(o.channels) > 1 {
		return invalid("reply-to cannot broadcast")
	}
	count := 0
	for _, file := range o.files {
		if file == "-" {
			count++
		}
	}
	if count > 1 {
		return invalid("stdin may appear only once")
	}
	return o, 0, true
}
func validateProducerInput(raw []byte, o producerOptions) (producerInput, []string, error) {
	var input producerInput
	if len(raw) > maxProduceRecord {
		return input, nil, errors.New("input record exceeds 64 KiB")
	}
	if !utf8.Valid(raw) {
		return input, nil, errors.New("invalid UTF-8")
	}
	if o.inputFormat == "jsonl" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return input, nil, errors.New("invalid JSONL record")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return input, nil, errors.New("trailing JSON content")
		}
	} else {
		input.Text = string(raw)
		input.Reply = o.reply
		input.Action = o.action
	}
	if input.Action && o.transport != "irc" {
		return input, nil, errors.New("action messages require explicit --transport irc")
	}
	if input.Action && utf8.RuneCountInString(input.Text) > 491 {
		return input, nil, errors.New("IRC action exceeds 491 characters including required framing")
	}
	if err := producer.ValidateText(input.Text); err != nil {
		return input, nil, err
	}
	for _, value := range []string{input.Reply, input.ClientID} {
		if len(value) > 512 || textsafe.NeedsSanitizing(value) {
			return input, nil, errors.New("invalid record identifier")
		}
	}
	channels := []string(o.channels)
	if o.inputFormat == "jsonl" {
		if input.Channel == "" {
			if len(channels) != 1 {
				return input, nil, errors.New("JSONL requires channel or one default channel")
			}
			input.Channel = channels[0]
		}
		selected := dataOptions{channels: stringList{input.Channel}, format: "jsonl", diagnosticFormat: "text", connectTimeout: time.Second}
		if err := validateDataOptions(&selected); err != nil {
			return input, nil, err
		}
		input.Channel = selected.channels[0]
		if len(channels) > 0 {
			found := false
			for _, channel := range channels {
				if channel == input.Channel {
					found = true
				}
			}
			if !found {
				return input, nil, errors.New("record channel outside allowlist")
			}
		}
		channels = []string{input.Channel}
	}
	return input, channels, nil
}

func (r *runner) runProduce(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stopReading := context.WithCancel(ctx)
	defer stopReading()
	o, code, ok := parseProducer(args, stdout, stderr)
	if !ok {
		return code
	}
	if err := r.selectAccount(o.account); err != nil {
		dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", err)
		return 2
	}
	if o.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.duration)
		defer cancel()
	}
	stdout, stderr = dataStreams(ctx, stdout, stderr)
	if len(o.messages) == 0 && (len(o.files) == 0 || containsProducerStdin(o.files)) {
		if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			dataDiagnostic(stderr, o.dataOptions, "invalid_arguments", errors.New("provide --message, --file, or piped stdin"))
			return 2
		}
	}
	// Explicit finite arguments are validated before any authentication or send.
	for _, message := range o.messages {
		if _, _, err := validateProducerInput([]byte(message), o); err != nil {
			dataDiagnostic(stderr, o.dataOptions, "invalid_input", err)
			return 2
		}
	}
	var delivery *producer.Service
	if !o.dryRun {
		cfg, err := config.Load(os.Environ(), config.Overrides{ConfigPath: o.configPath})
		if err != nil {
			dataDiagnostic(stderr, o.dataOptions, "config_error", err)
			return 7
		}
		credentialStatus, credentialErr := r.applyStoredCredentials(ctx, &cfg)
		if err = credentialErr; err != nil {
			dataDiagnostic(stderr, o.dataOptions, "credentials_error", err)
			return 7
		}
		if strings.TrimSpace(cfg.Twitch.ClientID) == "" || o.authMode == "app" && strings.TrimSpace(cfg.Twitch.ClientSecret) == "" || o.authMode != "app" && strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
			dataDiagnostic(stderr, o.dataOptions, "credentials_missing", errors.New("producer requires OAuth token and Twitch client ID; run twi login --capability chat-send"))
			return 3
		}
		delivery, err = r.producerDelivery(ctx, cfg, credentialStatus, o)
		if err != nil {
			dataDiagnostic(stderr, o.dataOptions, "auth_failed", err)
			return producerAuthErrorCode(err)
		}
	}
	if delivery != nil {
		defer delivery.Close()
	}
	successes, firstFailure, index := 0, 0, 0
	outputOrStorageFailure := false
	exit := func(failure int) int {
		if outputOrStorageFailure {
			return 7
		}
		if successes > 0 && failure != 0 {
			return 8
		}
		if firstFailure != 0 {
			if successes > 0 {
				return 8
			}
			return firstFailure
		}
		return failure
	}
	process := func(raw []byte) bool {
		index++
		if len(raw) == 0 && o.skipEmpty {
			return true
		}
		input, channels, err := validateProducerInput(raw, o)
		if err != nil {
			channels = []string{""}
		}
		for _, channel := range channels {
			if ctx.Err() != nil {
				if firstFailure == 0 {
					firstFailure = 4
				}
				return false
			}
			receipt := automation.SendResult{SchemaVersion: 1, Type: "send_result", InputIndex: index, ClientID: input.ClientID, Channel: automation.Channel{Login: channel}, Status: "validated", ObservedAt: time.Now().UTC()}
			failure := 0
			if err != nil {
				receipt.Status = "invalid"
				failure = 2
				receipt.DropReason = &automation.DropReason{Code: "invalid_input", Message: err.Error()}
			}
			if failure == 0 && !o.dryRun {
				receipt, failure = delivery.DeliverAction(ctx, channel, input.Text, input.Reply, input.Action)
				receipt.InputIndex = index
				receipt.ClientID = input.ClientID
				if failure == 7 {
					outputOrStorageFailure = true
				}
			}
			receipt.Transport = o.transport
			receipt.Authorization = o.authMode
			if o.authMode == "app" {
				sourceOnly := o.sharedChat != "all"
				receipt.ForSourceOnly = &sourceOnly
			}
			receipt.ObservedAt = time.Now().UTC()
			var outputErr error
			if o.format == "jsonl" {
				outputErr = json.NewEncoder(producerCheckedWriter{stdout}).Encode(receipt)
			} else {
				detail := receipt.MessageID
				if receipt.DropReason != nil {
					detail = receipt.DropReason.Code
				}
				_, outputErr = fmt.Fprintf(producerCheckedWriter{stdout}, "%d #%s %s %s\n", index, textsafe.Display(channel), receipt.Status, textsafe.Display(detail))
			}
			if outputErr != nil {
				dataDiagnostic(stderr, o.dataOptions, "output_failed", errors.New("cannot write producer receipt"))
				firstFailure = 7
				outputOrStorageFailure = true
				return false
			}
			if failure != 0 {
				if firstFailure == 0 {
					firstFailure = failure
				}
				if o.onError == "stop" {
					return false
				}
			} else {
				successes++
			}
		}
		return true
	}
	if len(o.messages) > 0 {
		for _, message := range o.messages {
			if ctx.Err() != nil {
				return exit(4)
			}
			if !process([]byte(message)) {
				return exit(firstFailure)
			}
		}
		return exit(0)
	}
	files := o.files
	if len(files) == 0 {
		files = stringList{"-"}
	}
	for _, path := range files {
		reader := stdin
		var file *os.File
		if path != "-" {
			var err error
			file, err = os.Open(path)
			if err != nil {
				dataDiagnostic(stderr, o.dataOptions, "input_error", errors.New("cannot open producer input file"))
				return exit(7)
			}
			reader = file
		}

		records := make(chan producerRead, 1)
		go readProducerRecords(ctx, reader, o.delimiter, records)
		var scanErr error
		reading := true
		for reading {
			select {
			case <-ctx.Done():
				if file != nil {
					_ = file.Close()
				}
				return exit(4)
			case record, open := <-records:
				if !open {
					reading = false
					break
				}
				if record.err != nil {
					scanErr = record.err
					reading = false
					break
				}
				if !process(record.raw) {
					if file != nil {
						_ = file.Close()
					}
					return exit(firstFailure)
				}
			}
		}
		if file != nil {
			closeErr := file.Close()
			if scanErr == nil {
				scanErr = closeErr
			}
		}
		if scanErr != nil {
			if errors.Is(scanErr, bufio.ErrTooLong) {
				process(make([]byte, maxProduceRecord+1))
				dataDiagnostic(stderr, o.dataOptions, "invalid_input", errors.New("input record exceeds 64 KiB"))
				if firstFailure == 7 {
					return 7
				}
				return exit(2)
			}
			dataDiagnostic(stderr, o.dataOptions, "input_error", errors.New("producer input read failed"))
			return exit(7)
		}

	}
	return exit(0)
}
func containsProducerStdin(files []string) bool {
	for _, file := range files {
		if file == "-" {
			return true
		}
	}
	return false
}
func splitProducerNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type producerRead struct {
	raw []byte
	err error
}

func readProducerRecords(ctx context.Context, reader io.Reader, delimiter string, out chan<- producerRead) {
	defer close(out)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxProduceRecord+2)
	if delimiter == "nul" {
		scanner.Split(splitProducerNUL)
	}
	for scanner.Scan() {
		record := producerRead{raw: append([]byte(nil), scanner.Bytes()...)}
		select {
		case out <- record:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case out <- producerRead{err: err}:
		case <-ctx.Done():
		}
	}
}

type producerCheckedWriter struct{ io.Writer }

func (w producerCheckedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func producerAuthErrorCode(err error) int { return producer.AuthErrorCode(err) }

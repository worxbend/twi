package debuglog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/w0rxbend/twi/internal/auth"
)

const Redacted = auth.RedactedSecret

var rawURLPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// Options configures an opt-in debug logger. Secrets are explicit raw values
// that must be removed from every string or error field before writing.
type Options struct {
	Enabled bool
	Secrets []auth.Secret
}

// Logger writes redacted JSON debug records. The zero value is disabled.
type Logger struct {
	enabled  bool
	logger   *slog.Logger
	redactor auth.Redactor
	secrets  []auth.Secret
}

// New returns a JSON debug logger. Passing Enabled=false or a nil writer
// returns a disabled logger.
func New(w io.Writer, opts Options) Logger {
	if !opts.Enabled || w == nil {
		return Logger{}
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	return Logger{
		enabled:  true,
		logger:   slog.New(handler),
		redactor: auth.NewRedactor(opts.Secrets...),
		secrets:  append([]auth.Secret(nil), opts.Secrets...),
	}
}

// Enabled reports whether this logger will write records.
func (l Logger) Enabled() bool {
	return l.enabled && l.logger != nil
}

// WithSecrets returns a copy of l that also redacts the provided secrets.
func (l Logger) WithSecrets(secrets ...auth.Secret) Logger {
	if len(secrets) == 0 {
		return l
	}
	l.secrets = append(append([]auth.Secret(nil), l.secrets...), secrets...)
	l.redactor = auth.NewRedactor(l.secrets...)
	return l
}

// Redact sanitizes a string for debug output.
func (l Logger) Redact(value string) string {
	if value == "" {
		return ""
	}
	value = l.redactor.Redact(value)
	return redactURLsInText(value)
}

// Log writes one structured debug record with an event name and sanitized
// attributes. It is intentionally a no-op when logging is disabled.
func (l Logger) Log(ctx context.Context, event string, attrs ...slog.Attr) {
	if !l.Enabled() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	safeAttrs := make([]slog.Attr, 0, len(attrs)+1)
	safeAttrs = append(safeAttrs, slog.String("event", l.Redact(event)))
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		safeAttrs = append(safeAttrs, l.sanitizeAttr(attr))
	}
	l.logger.LogAttrs(ctx, slog.LevelDebug, "debug", safeAttrs...)
}

// Err returns a string attribute from err.Error(). The value is redacted again
// by Logger.Log before it is written.
func Err(key string, err error) slog.Attr {
	if err == nil {
		return slog.String(key, "")
	}
	return slog.String(key, err.Error())
}

// URLFields returns source URL diagnostics without including the raw URL.
func URLFields(prefix, raw string) []slog.Attr {
	raw = strings.TrimSpace(raw)
	fields := []slog.Attr{slog.Bool(prefix+"_present", raw != "")}
	if raw == "" {
		return fields
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		fields = append(fields, slog.Bool(prefix+"_parse_error", true))
		return fields
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if scheme != "" {
		fields = append(fields, slog.String(prefix+"_scheme", scheme))
	}
	if host != "" {
		fields = append(fields, slog.String(prefix+"_host", host))
	}
	fields = append(fields,
		slog.Bool(prefix+"_has_userinfo", parsed.User != nil),
		slog.Bool(prefix+"_has_credential_marker", urlHasCredentialMarker(parsed)),
	)
	return fields
}

func (l Logger) sanitizeAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(l.Redact(attr.Value.String()))
	case slog.KindGroup:
		group := attr.Value.Group()
		safe := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			if child.Key == "" {
				continue
			}
			safe = append(safe, l.sanitizeAttr(child))
		}
		attr.Value = slog.GroupValue(safe...)
	case slog.KindAny:
		attr.Value = slog.StringValue(l.safeAnyValue(attr.Value.Any()))
	}
	return attr
}

func (l Logger) safeAnyValue(value any) string {
	if value == nil {
		return ""
	}
	if err, ok := value.(error); ok {
		return l.Redact(err.Error())
	}
	typ := reflect.TypeOf(value)
	if typ == nil {
		return ""
	}
	return fmt.Sprintf("<%s>", typ.String())
}

func redactURLsInText(value string) string {
	return rawURLPattern.ReplaceAllStringFunc(value, redactURLToken)
}

func redactURLToken(value string) string {
	trimmed := strings.TrimRight(value, ".,);]")
	suffix := strings.TrimPrefix(value, trimmed)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.User == nil {
		return value
	}
	clone := *parsed
	clone.User = url.User(Redacted)
	return clone.String() + suffix
}

func urlHasCredentialMarker(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	if parsed.User != nil {
		return true
	}
	values := []string{parsed.RawQuery, parsed.EscapedPath(), parsed.Fragment}
	for _, value := range values {
		if containsCredentialMarker(value) {
			return true
		}
		if unescaped, err := url.QueryUnescape(value); err == nil && containsCredentialMarker(unescaped) {
			return true
		}
		if unescaped, err := url.PathUnescape(value); err == nil && containsCredentialMarker(unescaped) {
			return true
		}
	}
	return false
}

func containsCredentialMarker(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	markers := []string{
		"oauth:",
		"access_token",
		"oauth_token",
		"refresh_token",
		"client_secret",
		"authorization_code",
		"code_verifier",
		"code_challenge",
		"authorization",
		"bearer ",
		"state=",
		"state:",
		"code=",
		"code:",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

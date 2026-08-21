// Package doctor implements `twi doctor`: a read-only check of whether this
// machine is set up to run twi, and an explanation of anything that is not.
//
// It lives outside internal/app because it is not part of the terminal UI. It
// draws nothing, holds no shell state, and imports neither Bubble Tea nor
// lipgloss -- it reads configuration, credentials, the terminal environment
// and the filesystem, and returns a Report that the CLI prints. Keeping it in
// the UI package meant those 600 lines were rebuilt and reasoned about
// alongside chat rendering, and it was the only reason internal/app depended
// on internal/storage at all.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
)

// redactedMarker is what twi's diagnostic output prints in place of a
// credential. internal/auth uses "<redacted>" for the same purpose; this
// package keeps the bracket form because it is what `twi doctor` and
// `twi config show` have always printed.
const redactedMarker = "[redacted]"

type Status string

type Report struct {
	Checks []Check
}

type Check struct {
	Name   string
	Status Status
	Detail string
}

type Options struct {
	Environ           []string
	CacheDir          string
	ConfigLoadError   error
	ReachabilityProbe ReachabilityProbe
	TokenValidator    twitch.TokenValidator
}

type ReachabilityProbe func(context.Context) error

func Run(ctx context.Context, cfg config.Config) Report {
	return RunWithOptions(ctx, cfg, Options{
		Environ:           os.Environ(),
		ReachabilityProbe: ProbeTwitchIRCReachability,
	})
}

func RunWithOptions(ctx context.Context, cfg config.Config, opts Options) Report {
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	if opts.ReachabilityProbe == nil {
		opts.ReachabilityProbe = ProbeTwitchIRCReachability
	}

	checks := []Check{
		configPathCheck(cfg.Path, opts.ConfigLoadError),
		usernameCheck(cfg.Twitch.Username),
		credentialCheck("oauth token", cfg.Twitch.OAuthToken, "live chat unavailable until TWI_TWITCH_OAUTH_TOKEN or TWITCH_ACCESS_TOKEN is set"),
		credentialCheck("refresh token", cfg.Twitch.RefreshToken, "auth refresh unavailable until TWI_TWITCH_REFRESH_TOKEN or TWITCH_REFRESH_TOKEN is set"),
		credentialCheck("client id", cfg.Twitch.ClientID, "optional Helix/API features unavailable"),
		clientSecretCheck(cfg.Twitch),
		channelsCheck(cfg.DefaultChannels),
		tokenValidationCheck(ctx, cfg, opts.TokenValidator),
		reachabilityCheck(ctx, opts.ReachabilityProbe),
		terminalCheck(opts.Environ),
		colorCheck(opts.Environ),
		mouseCheck(opts.Environ),
		cacheCheck(opts.CacheDir),
		legacyAssetCacheCheck(opts.CacheDir),
		featureModesCheck(cfg.Features),
		streamStatusCheck(cfg),
	}

	for i := range checks {
		checks[i].Detail = redactSensitive(checks[i].Detail, cfg)
	}
	return Report{Checks: checks}
}

func ProbeTwitchIRCReachability(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	dialer := net.Dialer{Timeout: 800 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", "irc.chat.twitch.tv:6697")
	if err != nil {
		return err
	}
	return conn.Close()
}

func configPathCheck(path string, loadErr error) Check {
	if strings.TrimSpace(path) == "" {
		return warnCheck("config file", "path unavailable")
	}
	displayPath := config.RedactDisplayValue(path)
	if loadErr != nil {
		return warnCheck("config file", fmt.Sprintf("%s (load failed: %s; using env/defaults)", displayPath, config.RedactDisplayValue(loadErr.Error())))
	}
	err := storage.CheckReadableFile(path)
	switch {
	case err == nil:
		return okCheck("config file", fmt.Sprintf("%s (readable)", displayPath))
	case errors.Is(err, storage.ErrPathIsDirectory):
		return warnCheck("config file", fmt.Sprintf("%s is a directory", displayPath))
	case errors.Is(err, os.ErrNotExist):
		return warnCheck("config file", fmt.Sprintf("%s (not found; using env/defaults)", displayPath))
	default:
		return warnCheck("config file", fmt.Sprintf("%s (%s)", displayPath, config.RedactDisplayValue(err.Error())))
	}
}

// usernameCheck reports the configured login as optional context rather than
// a requirement.
//
// twi derives the IRC login from the OAuth token itself, so a missing
// twitch_username costs nothing; the config value is only a fallback for when
// Twitch's validation endpoint is unreachable. Doctor used to say live chat
// was "unavailable until TWI_TWITCH_USERNAME is set", which sent people to
// configure something that has not been required since the login became
// token-derived.
func usernameCheck(username string) Check {
	if strings.TrimSpace(username) == "" {
		return okCheck("twitch username", "not set; the login is derived from the OAuth token")
	}
	return okCheck("twitch username", "set; used only as a fallback if token validation is unreachable")
}

func credentialCheck(name, value, missingDetail string) Check {
	if strings.TrimSpace(value) == "" {
		return warnCheck(name, "missing; "+missingDetail)
	}
	return okCheck(name, "present")
}

func channelsCheck(channels []string) Check {
	switch len(channels) {
	case 0:
		// Not an error: twi starts on the empty state and /channels opens the
		// first one. Still worth naming the persistent options.
		return okCheck("channels", "none configured; open one with /channels, or set --channel/--channels/TWI_DEFAULT_CHANNELS")
	case 1:
		return okCheck("channels", "one configured")
	default:
		return okCheck("channels", fmt.Sprintf("%d configured", len(channels)))
	}
}

func tokenValidationCheck(ctx context.Context, cfg config.Config, validator twitch.TokenValidator) Check {
	// Every result of this check is reported under the same name, and every
	// one but the last is a warning, so both are bound once here.
	warn := func(parts ...string) Check {
		return warnCheck("token validation", joinTokenValidationDetails(parts...))
	}

	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		return warn("skipped; OAuth token missing")
	}
	if validator == nil {
		return warn("not available; required scopes " + tokenScopesCSV(twitch.RequiredIRCScopes()) + " were not verified")
	}

	validation, err := validator.ValidateToken(ctx, tokenCredentialsFromConfig(cfg.Twitch))
	if err != nil {
		outcome := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
		return warn(fmt.Sprintf("%s: %v; token identity, expiry, and scopes were not verified", outcome, err))
	}

	// capabilities is what the token may do and for how long. summary
	// prefixes that with who it belongs to, and detailed puts a leading
	// explanation in front of the whole thing -- between them they build
	// every result below, which previously repeated these four lines five
	// times over.
	capabilities := func() []string {
		return []string{
			tokenScopeDetail("granted scopes", validation.Scopes),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		}
	}
	summary := func() []string {
		return append([]string{tokenIdentityDetail(validation.Identity)}, capabilities()...)
	}
	detailed := func(lead string) []string {
		return append([]string{lead}, summary()...)
	}
	// missingScopesDetail names the scopes the token lacks, preferring what
	// Twitch itself reported over what twi worked out from the granted list.
	missing := validation.MissingScopes
	if len(missing) == 0 {
		missing = twitch.MissingRequiredIRCScopes(validation.Scopes)
	}

	if validation.Status == twitch.TokenValidationValid && staleUsername(cfg.Twitch.Username, validation.Identity.Login) {
		return warn(staleUsernameDetail(cfg.Twitch.Username, validation.Identity.Login))
	}

	switch validation.Status {
	case twitch.TokenValidationValid:
	case twitch.TokenValidationMalformed:
		return warn(
			tokenValidationDetail(validation, "malformed OAuth token"),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		)
	case twitch.TokenValidationExpired:
		return warn(
			tokenValidationDetail(validation, "OAuth token expired"),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		)
	case twitch.TokenValidationWrongUser:
		// Not a blocker: live chat authenticates as the token's own account
		// and ignores a stale twitch_username, so this only flags config that
		// no longer matches reality.
		return warn(detailed(staleUsernameDetail(cfg.Twitch.Username, validation.Identity.Login))...)
	case twitch.TokenValidationMissingScope:
		lead := tokenValidationDetail(validation, "missing required IRC scope")
		if len(missing) > 0 {
			lead = "missing required scopes: " + tokenScopesCSV(missing)
		}
		return warn(detailed(lead)...)
	default:
		return warn(tokenValidationDetail(validation, "token validation returned unknown state"))
	}

	// The token is valid; the only thing that can still make this a warning
	// is a required scope that was never granted.
	if len(missing) > 0 {
		return warn(detailed("missing required scopes: " + tokenScopesCSV(missing))...)
	}
	return okCheck("token validation", joinTokenValidationDetails(append(
		[]string{
			tokenIdentityDetail(validation.Identity),
			"required scopes present: " + tokenScopesCSV(twitch.RequiredIRCScopes()),
		},
		capabilities()...)...))
}

func reachabilityCheck(ctx context.Context, probe ReachabilityProbe) Check {
	if probe == nil {
		return warnCheck("twitch reachability", "not checked")
	}
	if err := probe(ctx); err != nil {
		return warnCheck("twitch reachability", fmt.Sprintf("irc.chat.twitch.tv:6697 unreachable: %v", err))
	}
	return okCheck("twitch reachability", "irc.chat.twitch.tv:6697 reachable")
}

func terminalCheck(environ []string) Check {
	term := envMap(environ)["TERM"]
	switch {
	case term == "":
		return warnCheck("terminal", "TERM missing; terminal capability detection is limited")
	case term == "dumb":
		return warnCheck("terminal", "TERM=dumb; rich TUI features may be unavailable")
	default:
		return okCheck("terminal", "TERM="+term)
	}
}

func colorCheck(environ []string) Check {
	env := envMap(environ)
	term := env["TERM"]
	colorTerm := strings.ToLower(env["COLORTERM"])
	switch {
	case strings.Contains(colorTerm, "truecolor"), strings.Contains(colorTerm, "24bit"):
		return okCheck("terminal color", "true-color signal via COLORTERM")
	case strings.Contains(term, "truecolor"), strings.Contains(term, "24bit"), strings.Contains(term, "direct"):
		return okCheck("terminal color", "true-color signal via TERM")
	case strings.Contains(term, "256color"):
		return okCheck("terminal color", "256-color signal via TERM")
	default:
		return warnCheck("terminal color", "no true-color or 256-color signal; colors will use conservative fallbacks")
	}
}

func mouseCheck(environ []string) Check {
	term := envMap(environ)["TERM"]
	if term == "" || term == "dumb" {
		return warnCheck("terminal mouse", "mouse support unknown; keyboard controls remain primary")
	}
	return okCheck("terminal mouse", "terminal advertises interactive capabilities; mouse behavior remains optional")
}

func cacheCheck(cacheDir string) Check {
	if strings.TrimSpace(cacheDir) == "" {
		defaultDir, err := config.DefaultCacheDir()
		if err != nil {
			return warnCheck("cache directory", fmt.Sprintf("path unavailable: %v", err))
		}
		cacheDir = defaultDir
	}
	if err := storage.ProbeWritableDir(cacheDir); err != nil {
		return warnCheck("cache directory", fmt.Sprintf("%s not writable: %v", cacheDir, err))
	}
	return okCheck("cache directory", cacheDir+" writable")
}

// legacyAssetCacheCheck reports a leftover image cache from a removed feature.
//
// twi used to download avatars, emotes and emoji and render them as inline
// images, caching them on disk under <cache>/assets. That pipeline was removed
// as a product decision (ADR 0003, superseded) and everything now renders as
// text, so nothing writes there any more -- but an installation upgraded from
// an older version can still be holding up to a few hundred megabytes of files
// that will never be read again.
//
// This only reports. `twi doctor` is a diagnostic command, and deleting a
// user's files as a side effect of asking for a report would be a surprise;
// the check says what to remove and leaves the choice to the reader.
func legacyAssetCacheCheck(cacheDir string) Check {
	assetDir, err := legacyAssetCacheDir(cacheDir)
	if err != nil {
		return okCheck("legacy asset cache", "no cache directory to check")
	}
	info, err := os.Stat(assetDir)
	if errors.Is(err, os.ErrNotExist) {
		return okCheck("legacy asset cache", "none present")
	}
	if err != nil {
		return warnCheck("legacy asset cache", fmt.Sprintf("%s could not be read: %v", assetDir, err))
	}
	if !info.IsDir() {
		return okCheck("legacy asset cache", "none present")
	}
	bytes, files := directorySize(assetDir)
	if files == 0 {
		return okCheck("legacy asset cache", "none present")
	}
	return warnCheck("legacy asset cache", fmt.Sprintf(
		"%s holds %d file(s), %d bytes left over from the removed image renderer; "+
			"nothing reads them and the directory can be deleted",
		assetDir, files, bytes))
}

// directorySize totals the regular files under root. An unreadable entry is
// skipped rather than failing the whole check: this is advisory, and a partial
// total still tells the reader there is something there.
func directorySize(root string) (bytes int64, files int) {
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // advisory: skip what cannot be read
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files
}

func legacyAssetCacheDir(cacheDir string) (string, error) {
	if strings.TrimSpace(cacheDir) != "" {
		return filepath.Join(cacheDir, "assets"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "twi", "assets"), nil
}

// staleUsernameDetail explains a configured username that disagrees with the
// token owner, and what twi will do about it.
func staleUsernameDetail(configured, actual string) string {
	configured = strings.TrimSpace(configured)
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return "OAuth token identity is unknown"
	}
	if configured == "" || strings.EqualFold(configured, actual) {
		return "OAuth token belongs to " + actual
	}
	return fmt.Sprintf(
		"configured twitch_username %q is stale; the token belongs to %q and live chat will connect as %q (channels are unaffected)",
		configured, actual, actual,
	)
}

func featureModesCheck(features config.FeatureConfig) Check {
	detail := fmt.Sprintf(
		"avatar=%s animation=%s theme=%s stream_status=%s emote_autocomplete=%s mouse=%t layout=%s badges=%s highlight_emotes=%t full_username=%t",
		features.AvatarMode,
		features.AnimationMode,
		features.ThemeName,
		features.StreamStatusMode,
		features.EmoteAutocompleteMode,
		features.EnableMouse,
		features.MessageLayout,
		features.BadgeMode,
		features.HighlightEmotes,
		features.FullUsername,
	)
	if unknown := unknownFeatureModes(features); len(unknown) > 0 {
		return warnCheck("feature modes", detail+"; unknown: "+strings.Join(unknown, ", "))
	}
	return okCheck("feature modes", detail)
}

func unknownFeatureModes(features config.FeatureConfig) []string {
	var unknown []string
	if !oneOf(features.AvatarMode, "off", "initials") {
		unknown = append(unknown, "avatar="+features.AvatarMode)
	}
	if !oneOf(features.AnimationMode, "off", "reduced", "fast") {
		unknown = append(unknown, "animation="+features.AnimationMode)
	}
	if strings.TrimSpace(features.ThemeName) != "" && !oneOf(features.ThemeName, append(theme.PresetNames(), "custom")...) {
		unknown = append(unknown, "theme="+features.ThemeName)
	}
	if !oneOf(features.StreamStatusMode, "auto", "off") {
		unknown = append(unknown, "stream_status="+features.StreamStatusMode)
	}
	if !oneOf(features.EmoteAutocompleteMode, "auto", "off") {
		unknown = append(unknown, "emote_autocomplete="+features.EmoteAutocompleteMode)
	}
	if !oneOf(features.MessageLayout, "inline", "grouped", "compact") {
		unknown = append(unknown, "message_layout="+features.MessageLayout)
	}
	if !oneOf(features.BadgeMode, "glyph", "text", "off") {
		unknown = append(unknown, "badge_mode="+features.BadgeMode)
	}
	return unknown
}

// streamStatusCheck reports whether the real-broadcast LIVE indicator can
// poll Twitch Helix "Get Streams" (see cli.newStreamStatusResolver): it
// needs stream_status_mode enabled plus a Client ID and OAuth token.
func streamStatusCheck(cfg config.Config) Check {
	if strings.EqualFold(strings.TrimSpace(cfg.Features.StreamStatusMode), "off") {
		return warnCheck("stream status polling", "disabled by stream_status_mode=off; the LIVE indicator will show OFFLINE")
	}
	var missing []string
	if strings.TrimSpace(cfg.Twitch.ClientID) == "" {
		missing = append(missing, "twitch_client_id")
	}
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		missing = append(missing, "twitch_oauth_token")
	}
	if len(missing) > 0 {
		return warnCheck("stream status polling", "unavailable until "+strings.Join(missing, " and ")+" is set; the LIVE indicator will show OFFLINE")
	}
	return okCheck("stream status polling", "Twitch Helix Get Streams is configured; LIVE reflects real broadcast status")
}

func tokenCredentialsFromConfig(cfg config.TwitchConfig) twitch.TokenCredentials {
	return twitch.TokenCredentials{
		Username:     cfg.Username,
		OAuthToken:   cfg.OAuthToken,
		RefreshToken: cfg.RefreshToken,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	}
}

func tokenValidationDetail(validation twitch.TokenValidationResult, fallback string) string {
	if detail := strings.TrimSpace(validation.Detail); detail != "" {
		return detail
	}
	return fallback
}

// clientSecretCheck reports whether unattended token refresh can actually
// run, which is not the same question as whether a secret is configured.
//
// Twitch access tokens expire after roughly four hours. twi refreshes on auth
// failure, but the refresh flow needs the client secret, and `twi login` does
// not save one: the credential store holds the refresh token and client ID
// and nothing else. So the common setup -- log in, start chatting -- has a
// refresh token that can never be redeemed, and chat drops mid-session with
// nothing having warned about it. That deserves a warning here rather than
// the old "optional OAuth client-secret flow unavailable".
func clientSecretCheck(cfg config.TwitchConfig) Check {
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		return okCheck("client secret", "set; unattended token refresh can run")
	}
	if strings.TrimSpace(cfg.RefreshToken) == "" {
		return warnCheck("client secret", "not set; optional OAuth client-secret flow unavailable")
	}
	return warnCheck("client secret", "not set, so the saved refresh token cannot be redeemed: "+
		"live chat will disconnect when the access token expires (about 4 hours) and will not recover on its own. "+
		"Set TWI_TWITCH_CLIENT_SECRET to the secret from your Twitch application, or re-run `twi login` when chat drops")
}

// refreshAvailabilityDetail names which credential is missing rather than
// reporting a bare "unavailable", because the three inputs come from three
// different places (login, config, the Twitch developer console) and knowing
// which one is absent is the whole difference between an actionable warning
// and a shrug. The validator is the authority on whether refresh works, since
// it saw the credentials actually used.
func refreshAvailabilityDetail(available bool, cfg config.TwitchConfig) string {
	if available {
		return "refresh credentials are available"
	}
	var missing []string
	if strings.TrimSpace(cfg.RefreshToken) == "" {
		missing = append(missing, "refresh token")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		missing = append(missing, "client id")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		missing = append(missing, "client secret")
	}
	if len(missing) == 0 {
		return "refresh credentials are unavailable"
	}
	return "refresh credentials are unavailable (missing " + strings.Join(missing, ", ") + ")"
}

func tokenIdentityDetail(identity twitch.TokenIdentity) string {
	login := strings.TrimSpace(identity.Login)
	userID := strings.TrimSpace(identity.UserID)
	displayName := strings.TrimSpace(identity.DisplayName)
	switch {
	case login == "" && userID == "" && displayName == "":
		return "identity unavailable"
	case login != "" && userID != "":
		return fmt.Sprintf("identity %s (id %s)", login, userID)
	case login != "":
		return "identity " + login
	case displayName != "":
		return "identity " + displayName
	default:
		return "identity id " + userID
	}
}

func tokenScopeDetail(label string, scopes []twitch.TokenScope) string {
	if len(scopes) == 0 {
		return label + " unavailable"
	}
	return label + ": " + tokenScopesCSV(scopes)
}

func tokenExpiryDetail(expiresAt time.Time) string {
	if expiresAt.IsZero() {
		return "expiry unavailable"
	}
	return "expires at " + expiresAt.UTC().Format(time.RFC3339)
}

func joinTokenValidationDetails(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "; ")
}

// staleUsername reports a configured username that names a different account
// than the token's owner. An unset username is not stale - it is the
// recommended configuration, since the login is derived from the token.
func staleUsername(configured, actual string) bool {
	configured = strings.TrimSpace(configured)
	actual = strings.TrimSpace(actual)
	return configured != "" && actual != "" && !strings.EqualFold(configured, actual)
}

func tokenScopesCSV(scopes []twitch.TokenScope) string {
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	return strings.Join(values, ", ")
}

// redactSensitive makes a diagnostic line safe to print.
//
// The rules live in internal/auth, which owns this policy for the whole
// program; this package used to carry its own copy of the patterns. The
// placeholder stays "[redacted]" because that is what the rest of twi's
// diagnostic output already prints, and keeping a surface's own marker is
// exactly the reason WithPlaceholder exists -- previously it was the reason
// this package kept its own patterns, which is how they drifted.
func redactSensitive(detail string, cfg config.Config) string {
	values := make([]auth.Secret, 0, 4)
	for _, secret := range sensitiveValues(cfg) {
		values = append(values, auth.NewSecret(secret))
	}
	return auth.NewRedactor(values...).WithPlaceholder(redactedMarker).Redact(detail)
}

func sensitiveValues(cfg config.Config) []string {
	values := []string{cfg.Twitch.OAuthToken, cfg.Twitch.RefreshToken, cfg.Twitch.ClientSecret}
	token := strings.TrimSpace(cfg.Twitch.OAuthToken)
	if prefix, body, ok := strings.Cut(token, ":"); ok && strings.EqualFold(prefix, "oauth") {
		values = append(values, body)
	}
	return values
}

func envMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func oneOf(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

func okCheck(name, detail string) Check {
	return Check{Name: name, Status: StatusOK, Detail: detail}
}

func warnCheck(name, detail string) Check {
	return Check{Name: name, Status: StatusWarn, Detail: detail}
}

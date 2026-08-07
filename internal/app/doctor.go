package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
)

const (
	DoctorStatusOK   DoctorStatus = "ok"
	DoctorStatusWarn DoctorStatus = "warn"
)

var oauthPattern = regexp.MustCompile(`(?i)oauth:[^\s]+`)
var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s"'&?]+`)
var credentialPairPattern = regexp.MustCompile(`(?i)((?:access[_-]token|oauth[_-]token|refresh[_-]token|client[_-]secret|authorization_code|code_verifier|code_challenge|state|code)(?:["']?\s*[:=]\s*["']?))[^"'\s&?]+`)

type DoctorStatus string

type DoctorReport struct {
	Checks []DoctorCheck
}

type DoctorCheck struct {
	Name   string
	Status DoctorStatus
	Detail string
}

type DoctorOptions struct {
	Environ           []string
	CacheDir          string
	ConfigLoadError   error
	ReachabilityProbe ReachabilityProbe
	TokenValidator    twitch.TokenValidator
}

type ReachabilityProbe func(context.Context) error

func Doctor(ctx context.Context, cfg config.Config) DoctorReport {
	return DoctorWithOptions(ctx, cfg, DoctorOptions{
		Environ:           os.Environ(),
		ReachabilityProbe: ProbeTwitchIRCReachability,
	})
}

func DoctorWithOptions(ctx context.Context, cfg config.Config, opts DoctorOptions) DoctorReport {
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	if opts.ReachabilityProbe == nil {
		opts.ReachabilityProbe = ProbeTwitchIRCReachability
	}

	checks := []DoctorCheck{
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
		cachePruningCheck(ctx, opts.CacheDir),
		featureModesCheck(cfg.Features),
		streamStatusCheck(cfg),
	}

	for i := range checks {
		checks[i].Detail = redactSensitive(checks[i].Detail, cfg)
	}
	return DoctorReport{Checks: checks}
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

func configPathCheck(path string, loadErr error) DoctorCheck {
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
func usernameCheck(username string) DoctorCheck {
	if strings.TrimSpace(username) == "" {
		return okCheck("twitch username", "not set; the login is derived from the OAuth token")
	}
	return okCheck("twitch username", "set; used only as a fallback if token validation is unreachable")
}

func credentialCheck(name, value, missingDetail string) DoctorCheck {
	if strings.TrimSpace(value) == "" {
		return warnCheck(name, "missing; "+missingDetail)
	}
	return okCheck(name, "present")
}

func channelsCheck(channels []string) DoctorCheck {
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

func tokenValidationCheck(ctx context.Context, cfg config.Config, validator twitch.TokenValidator) DoctorCheck {
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		return warnCheck("token validation", "skipped; OAuth token missing")
	}
	if validator == nil {
		return warnCheck("token validation", "not available; required scopes "+tokenScopesCSV(twitch.RequiredIRCScopes())+" were not verified")
	}

	credentials := tokenCredentialsFromConfig(cfg.Twitch)
	validation, err := validator.ValidateToken(ctx, credentials)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return warnCheck("token validation", fmt.Sprintf("canceled: %v; token identity, expiry, and scopes were not verified", err))
		}
		return warnCheck("token validation", fmt.Sprintf("failed: %v; token identity, expiry, and scopes were not verified", err))
	}

	missing := validation.MissingScopes
	if len(missing) == 0 {
		missing = twitch.MissingRequiredIRCScopes(validation.Scopes)
	}

	if validation.Status == twitch.TokenValidationValid && staleUsername(cfg.Twitch.Username, validation.Identity.Login) {
		return warnCheck("token validation", staleUsernameDetail(cfg.Twitch.Username, validation.Identity.Login))
	}

	switch validation.Status {
	case twitch.TokenValidationValid:
	case twitch.TokenValidationMalformed:
		return warnCheck("token validation", joinTokenValidationDetails(
			tokenValidationDetail(validation, "malformed OAuth token"),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		))
	case twitch.TokenValidationExpired:
		return warnCheck("token validation", joinTokenValidationDetails(
			tokenValidationDetail(validation, "OAuth token expired"),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		))
	case twitch.TokenValidationWrongUser:
		// Not a blocker: live chat authenticates as the token's own account
		// and ignores a stale twitch_username, so this only flags config that
		// no longer matches reality.
		return warnCheck("token validation", joinTokenValidationDetails(
			staleUsernameDetail(cfg.Twitch.Username, validation.Identity.Login),
			tokenIdentityDetail(validation.Identity),
			tokenScopeDetail("granted scopes", validation.Scopes),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		))
	case twitch.TokenValidationMissingScope:
		if len(missing) > 0 {
			return warnCheck("token validation", joinTokenValidationDetails(
				"missing required scopes: "+tokenScopesCSV(missing),
				tokenIdentityDetail(validation.Identity),
				tokenScopeDetail("granted scopes", validation.Scopes),
				tokenExpiryDetail(validation.ExpiresAt),
				refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
			))
		}
		return warnCheck("token validation", joinTokenValidationDetails(
			tokenValidationDetail(validation, "missing required IRC scope"),
			tokenIdentityDetail(validation.Identity),
			tokenScopeDetail("granted scopes", validation.Scopes),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		))
	default:
		return warnCheck("token validation", tokenValidationDetail(validation, "token validation returned unknown state"))
	}

	if len(missing) > 0 {
		return warnCheck("token validation", joinTokenValidationDetails(
			"missing required scopes: "+tokenScopesCSV(missing),
			tokenIdentityDetail(validation.Identity),
			tokenScopeDetail("granted scopes", validation.Scopes),
			tokenExpiryDetail(validation.ExpiresAt),
			refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
		))
	}
	return okCheck("token validation", joinTokenValidationDetails(
		tokenIdentityDetail(validation.Identity),
		"required scopes present: "+tokenScopesCSV(twitch.RequiredIRCScopes()),
		tokenScopeDetail("granted scopes", validation.Scopes),
		tokenExpiryDetail(validation.ExpiresAt),
		refreshAvailabilityDetail(validation.RefreshAvailable, cfg.Twitch),
	))
}

func reachabilityCheck(ctx context.Context, probe ReachabilityProbe) DoctorCheck {
	if probe == nil {
		return warnCheck("twitch reachability", "not checked")
	}
	if err := probe(ctx); err != nil {
		return warnCheck("twitch reachability", fmt.Sprintf("irc.chat.twitch.tv:6697 unreachable: %v", err))
	}
	return okCheck("twitch reachability", "irc.chat.twitch.tv:6697 reachable")
}

func terminalCheck(environ []string) DoctorCheck {
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

func colorCheck(environ []string) DoctorCheck {
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

func mouseCheck(environ []string) DoctorCheck {
	term := envMap(environ)["TERM"]
	if term == "" || term == "dumb" {
		return warnCheck("terminal mouse", "mouse support unknown; keyboard controls remain primary")
	}
	return okCheck("terminal mouse", "terminal advertises interactive capabilities; mouse behavior remains optional")
}

func cacheCheck(cacheDir string) DoctorCheck {
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

func cachePruningCheck(ctx context.Context, cacheDir string) DoctorCheck {
	assetDir, err := assetCacheDir(cacheDir)
	if err != nil {
		return warnCheck("asset cache pruning", fmt.Sprintf("path unavailable: %v", err))
	}

	pruneCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	report, err := storage.NewDiskAssetCache(assetDir).Prune(pruneCtx, storage.PruneOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return warnCheck("asset cache pruning", fmt.Sprintf("%s cleanup timed out: %v; chat can still start", assetDir, err))
		}
		return warnCheck("asset cache pruning", fmt.Sprintf("%s cleanup failed: %v; fix cache directory permissions or remove stale cache files", assetDir, err))
	}
	return okCheck("asset cache pruning", fmt.Sprintf(
		"%s checked; scanned=%d pruned=%d expired=%d size=%d bytes=%d/%d",
		report.Root,
		report.EntriesScanned,
		report.EntriesPruned,
		report.ExpiredPruned,
		report.SizePruned,
		report.BytesAfter,
		storage.DefaultAssetCacheMaxBytes,
	))
}

func assetCacheDir(cacheDir string) (string, error) {
	if strings.TrimSpace(cacheDir) != "" {
		return filepath.Join(cacheDir, "assets"), nil
	}
	return storage.DefaultAssetCacheDir()
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

func featureModesCheck(features config.FeatureConfig) DoctorCheck {
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
func streamStatusCheck(cfg config.Config) DoctorCheck {
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
func clientSecretCheck(cfg config.TwitchConfig) DoctorCheck {
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

func redactSensitive(detail string, cfg config.Config) string {
	detail = oauthPattern.ReplaceAllString(detail, "[redacted]")
	detail = bearerPattern.ReplaceAllString(detail, "${1}[redacted]")
	detail = credentialPairPattern.ReplaceAllString(detail, "${1}[redacted]")
	for _, secret := range sensitiveValues(cfg) {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	return detail
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

func okCheck(name, detail string) DoctorCheck {
	return DoctorCheck{Name: name, Status: DoctorStatusOK, Detail: detail}
}

func warnCheck(name, detail string) DoctorCheck {
	return DoctorCheck{Name: name, Status: DoctorStatusWarn, Detail: detail}
}

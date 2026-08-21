package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/app"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/doctor"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/theme"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
	"github.com/worxbend/twi/internal/twitch/irc"
)

const usage = `twi is a terminal Twitch chat client.

Usage:
  twi chat [--channel name] [--channels a,b] [--mock] [--debug-log]
  twi config show
  twi config path
  twi doctor
  twi login [--dry-run]
  twi profile list|show|set <name>
  twi setup

Environment:
  TWI_TWITCH_USERNAME
  TWI_TWITCH_OAUTH_TOKEN
  TWI_TWITCH_REFRESH_TOKEN
  TWI_TWITCH_CLIENT_ID
  TWI_TWITCH_CLIENT_SECRET
  TWI_TWITCH_REDIRECT_URL
  TWITCH_USERNAME
  TWITCH_ACCESS_TOKEN
  TWITCH_REFRESH_TOKEN
  TWITCH_CLIENT_ID
  TWITCH_CLIENT_SECRET
  TWITCH_REDIRECT_URL
  TWI_DEFAULT_CHANNELS
  TWI_ENABLE_MOUSE
  TWI_AVATAR_MODE
  TWI_ANIMATION_MODE
  TWI_THEME_NAME
  TWI_THEME_BACKGROUND
  TWI_THEME_FOREGROUND
  TWI_THEME_ACCENT
  TWI_THEME_MUTED
  TWI_THEME_BORDER
  TWI_THEME_SURFACE
  TWI_THEME_WARNING
  TWI_THEME_ERROR
  TWI_THEME_SUCCESS
  TWI_STREAM_STATUS_MODE
  TWI_EMOTE_AUTOCOMPLETE_MODE
  TWI_DEBUG_LOG
  TWI_DEBUG_LOG_PATH
`

var newLiveChatClient = func(ctx context.Context, cfg config.Config, holder *credentialHolder, logger debuglog.Logger, credentialStatus credentialLoadStatus) (app.ChatClient, error) {
	return app.NewRestartableLiveChatClientWithOptions(ctx, liveIRCTransportFactory(cfg, holder, logger, credentialStatus), 0, app.LiveChatClientOptions{
		DebugLogger: logger,
	})
}

// liveIRCTransportFactory builds a transport from whatever credentials are
// current, not from the ones present at startup.
//
// The factory is called again for every manual reconnect, and a refresh in
// between rotates both tokens. Reading through the holder is what makes ctrl+r
// work after a refresh; capturing cfg by value is what made it rebuild the
// client with credentials Twitch had already invalidated.
func liveIRCTransportFactory(cfg config.Config, holder *credentialHolder, logger debuglog.Logger, credentialStatus credentialLoadStatus) app.LiveChatTransportFactory {
	return func(context.Context) (twitch.ChatClient, error) {
		return irc.NewClient(liveIRCConfig(cfg, holder, logger, credentialStatus))
	}
}

// liveIRCConfig assembles the transport config from the credentials that are
// current right now, which is the whole point of the holder.
func liveIRCConfig(cfg config.Config, holder *credentialHolder, logger debuglog.Logger, credentialStatus credentialLoadStatus) irc.Config {
	creds := holder.current()
	return irc.Config{
		Username:     creds.Username,
		OAuthToken:   creds.OAuthToken,
		RefreshToken: creds.RefreshToken,
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Channels:     cfg.DefaultChannels,
		DebugLogger:  logger,
		OnOAuthRefresh: func(ctx context.Context, refreshed irc.OAuthRefresh) error {
			// Update the holder before persisting: a failed disk write must
			// not leave the process using dead credentials, and the in-memory
			// value is what the next reconnect reads.
			holder.applyRefresh(refreshed)
			return persistRefreshedIRCCredentials(ctx, holder.configWithCurrentCredentials(cfg), credentialStatus, refreshed)
		},
	}
}

// newLiveClientOptionsWithHolder builds the Helix-backed features against a
// live token source, so a mid-session refresh reaches them.
//
// Without it, every client froze the startup token at construction: after the
// IRC transport refreshed, chat kept working while the LIVE indicator,
// follower and subscriber counts, /clip, stream markers, Stream Info and the
// emote index all began returning 401 -- a failure mode that looks like
// several unrelated features breaking at once.
var newLiveClientOptionsWithHolder = func(cfg config.Config, holder *credentialHolder) app.ClientOptions {
	tokenSource := holder.tokenSource()
	return app.ClientOptions{
		StreamStatusResolver: newStreamStatusResolver(cfg, tokenSource),
		EmoteIndex:           newEmoteIndex(cfg, tokenSource),
		ChannelManager:       newChannelManager(cfg, tokenSource),
		GameLookup:           newGameLookup(cfg, tokenSource),
		UserLookup:           newUserLookup(cfg, tokenSource),
		MarkerManager:        newMarkerManager(cfg, tokenSource),
		FollowerLookup:       newFollowerLookup(cfg, tokenSource),
		SubscriptionLookup:   newSubscriptionLookup(cfg, tokenSource),
		ClipManager:          newClipManager(cfg, tokenSource),
		FollowedChannels:     newFollowedChannelLookup(cfg, tokenSource),
	}
}

const (
	// helixInteractiveTimeout bounds the Helix calls made while someone is
	// waiting on them -- emote search and the LIVE indicator -- where a slow
	// answer is worse than no answer.
	helixInteractiveTimeout = 2 * time.Second
	// helixBackgroundTimeout bounds the rest, which run behind the UI and
	// can afford to wait a little longer for a slow network.
	helixBackgroundTimeout = 5 * time.Second
)

// twitchAPIConfigured reports whether the credentials every Helix adapter
// needs are present.
//
// When they are not, the adapters are left nil and the features built on them
// stay quietly switched off, which is why every factory below checks this
// before constructing anything: twi is usable for reading chat with no Twitch
// API credentials at all, and it should not fill the screen with 401s to say
// so.
func twitchAPIConfigured(cfg config.Config) bool {
	return strings.TrimSpace(cfg.Twitch.ClientID) != "" &&
		strings.TrimSpace(cfg.Twitch.OAuthToken) != ""
}

// newHelixAdapter builds one Helix-backed adapter, or returns a nil T when
// Twitch API credentials are not configured.
//
// The eight factories below were the same three steps each -- check the
// credentials, fill in the same four settings, call the constructor -- so the
// steps live here and each factory now supplies only what differs: which
// constructor to call and how long to let its requests run.
//
// Returning T's zero value matters: T is always an interface here, and its
// zero value is a nil interface. Returning a nil *HelixSomethingClient
// instead would produce an interface that is non-nil but unusable, and every
// `if adapter == nil` guard in the app would stop working.
func newHelixAdapter[T any](cfg config.Config, tokenSource func() string, timeout time.Duration, build func(helix.ClientConfig) T) T {
	var unavailable T
	if !twitchAPIConfigured(cfg) {
		return unavailable
	}
	return build(helix.ClientConfig{
		HTTPClient:       &http.Client{Timeout: timeout},
		ClientID:         cfg.Twitch.ClientID,
		OAuthToken:       cfg.Twitch.OAuthToken,
		OAuthTokenSource: tokenSource,
	})
}

// newFollowedChannelLookup wires the /channels picker's autocomplete to the
// user's real follow list, gated on Twitch API credentials
// (user:read:follows is requested at login but Twitch still enforces it
// per-request; tokens issued before that scope existed simply fall back to
// already-open and configured channels).
func newFollowedChannelLookup(cfg config.Config, tokenSource func() string) twitch.FollowedChannelLookup {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.FollowedChannelLookup {
			return helix.NewFollowedChannelsClient(c)
		})
}

// newChannelManager wires the Stream Info tab's Get/Modify Channel
// Information calls to real Twitch Helix, gated only on Twitch API
// credentials (channel:manage:broadcast is requested at login but Twitch
// still enforces it per-request, so a missing grant simply surfaces as an
// API error on the tab rather than being pre-checked here).
func newChannelManager(cfg config.Config, tokenSource func() string) twitch.ChannelManager {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.ChannelManager { return helix.NewChannelsClient(c) })
}

// newGameLookup resolves a Stream Info category name to its Twitch game ID
// when the user changes the category field.
func newGameLookup(cfg config.Config, tokenSource func() string) twitch.GameLookup {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.GameLookup { return helix.NewGamesClient(c) })
}

// newUserLookup resolves Twitch user IDs by login: the logged-in user's own
// ID for Stream Info's Helix calls, and any active channel's broadcaster ID
// for channel-specific emote autocomplete.
func newUserLookup(cfg config.Config, tokenSource func() string) twitch.UserLookup {
	return newHelixAdapter(cfg, tokenSource, helixInteractiveTimeout,
		func(c helix.ClientConfig) twitch.UserLookup { return helix.NewUsersClient(c) })
}

// newMarkerManager wires the Misc tab's Create/Get Stream Marker calls to
// real Twitch Helix. Uses the same channel:manage:broadcast scope already
// requested for Stream Info, so no additional login scope is needed.
func newMarkerManager(cfg config.Config, tokenSource func() string) twitch.MarkerManager {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.MarkerManager { return helix.NewMarkersClient(c) })
}

// newClipManager wires the /clip chat command's Create Clip calls to real
// Twitch Helix. Requires the clips:edit scope, requested at login alongside
// the other tab scopes; Twitch still enforces it per-request.
func newClipManager(cfg config.Config, tokenSource func() string) twitch.ClipManager {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.ClipManager { return helix.NewClipsClient(c) })
}

// newFollowerLookup wires the status line's follower count to real Twitch
// Helix, gated on Twitch API credentials (moderator:read:followers is
// requested at login but Twitch still enforces it per-request).
func newFollowerLookup(cfg config.Config, tokenSource func() string) twitch.FollowerLookup {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.FollowerLookup { return helix.NewFollowersClient(c) })
}

// newSubscriptionLookup wires the status line's subscriber count to real
// Twitch Helix, gated on Twitch API credentials (channel:read:subscriptions
// is requested at login but Twitch still enforces it per-request).
func newSubscriptionLookup(cfg config.Config, tokenSource func() string) twitch.SubscriptionLookup {
	return newHelixAdapter(cfg, tokenSource, helixBackgroundTimeout,
		func(c helix.ClientConfig) twitch.SubscriptionLookup {
			return helix.NewSubscriptionsClient(c)
		})
}

// newEmoteIndex wires Ctrl+E emote search to real Twitch Helix emote data. EmoteIndex is in-memory only and needs no cache,
// just Client ID/OAuth token.
func newEmoteIndex(cfg config.Config, tokenSource func() string) *assets.EmoteIndex {
	if strings.EqualFold(strings.TrimSpace(cfg.Features.EmoteAutocompleteMode), "off") {
		return nil
	}
	if !twitchAPIConfigured(cfg) {
		return nil
	}
	return assets.NewEmoteIndex(helix.NewChatAssetsClient(helix.ChatAssetsClientConfig{
		HTTPClient:       &http.Client{Timeout: helixInteractiveTimeout},
		ClientID:         cfg.Twitch.ClientID,
		OAuthToken:       cfg.Twitch.OAuthToken,
		OAuthTokenSource: tokenSource,
	}))
}

// newStreamStatusResolver wires the real Twitch Helix "Get Streams" LIVE
// indicator, gated only on stream_status_mode and Twitch API credentials.
func newStreamStatusResolver(cfg config.Config, tokenSource func() string) twitch.StreamLookup {
	if strings.EqualFold(strings.TrimSpace(cfg.Features.StreamStatusMode), "off") {
		return nil
	}
	return newHelixAdapter(cfg, tokenSource, helixInteractiveTimeout,
		func(c helix.ClientConfig) twitch.StreamLookup { return helix.NewStreamsClient(c) })
}

var runLiveChat = app.RunClientWithOptions

var newDoctorTokenValidator = func() twitch.TokenValidator {
	return helix.NewOAuthTokenValidator(helix.OAuthTokenValidatorConfig{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
}

var newLiveTokenValidator = func() twitch.TokenValidator {
	return newDoctorTokenValidator()
}

var doctorReachabilityProbe = doctor.ProbeTwitchIRCReachability

var doctorCacheDir = func() string {
	return ""
}

var buildDoctorReport = func(ctx context.Context, cfg config.Config, cfgErr error) doctor.Report {
	return doctor.RunWithOptions(ctx, cfg, doctor.Options{
		CacheDir:          doctorCacheDir(),
		ConfigLoadError:   cfgErr,
		ReachabilityProbe: doctorReachabilityProbe,
		TokenValidator:    newDoctorTokenValidator(),
	})
}

var newCredentialStore = func() (storage.CredentialStore, error) {
	return storage.NewDefaultCredentialStore()
}

type credentialLoadStatus struct {
	Path                string
	Label               string
	Location            string
	Present             bool
	AccessTokenShadowed bool
	Err                 error
	Store               storage.CredentialStore
	Record              storage.CredentialRecord
}

// Run executes the command line entrypoint. It returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "chat":
		return runChat(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "login":
		return runLogin(args[1:], stdout, stderr)
	case "profile":
		return runProfile(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// chatOptions is the parsed command line of `twi chat`.
type chatOptions struct {
	channels   channelFlags
	configPath string
	mock       bool
	debugFlags debugFlagOptions
}

// parseChatFlags parses the `twi chat` command line. ok is false when the
// caller should return code immediately.
func parseChatFlags(args []string, stderr io.Writer) (opts chatOptions, code int, ok bool) {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&opts.channels, "channel", "Twitch channel to join; repeat for multiple channels")
	fs.Var(&opts.channels, "channels", "comma-separated Twitch channels to join (adds to --channel)")
	fs.StringVar(&opts.configPath, "config", "", "config file path")
	fs.BoolVar(&opts.mock, "mock", false, "run against the built-in mock chat source")
	addDebugFlags(fs, &opts.debugFlags)

	if err := fs.Parse(args); err != nil {
		return opts, 2, false
	}
	return opts, 0, true
}

// chatConfig loads the effective configuration for `twi chat`, with any
// channels named on the command line replacing the configured defaults.
func chatConfig(opts chatOptions, stderr io.Writer) (config.Config, bool) {
	overrides := config.Overrides{
		ConfigPath: opts.configPath,
		Channels:   []string(opts.channels),
	}
	applyDebugFlagOverrides(&overrides, opts.debugFlags)
	cfg, err := config.Load(os.Environ(), overrides)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return cfg, false
	}
	if len(opts.channels) > 0 {
		cfg.DefaultChannels = []string(opts.channels)
	}
	return cfg, true
}

func runChat(args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseChatFlags(args, stderr)
	if !ok {
		return code
	}
	cfg, ok := chatConfig(opts, stderr)
	if !ok {
		return 1
	}
	if opts.mock {
		return runMockChat(cfg, stdout, stderr)
	}
	return runLiveChatSession(cfg, stdout, stderr)
}

// runMockChat drives the shell against the built-in mock chat source, which
// needs no credentials and no network. It is how the UI is exercised without
// a Twitch account.
func runMockChat(cfg config.Config, stdout, stderr io.Writer) int {
	logger, closeLog, ok := openDebugLoggerOrReport(cfg, stderr)
	if !ok {
		return 1
	}
	defer closeLog()
	logger.Log(context.Background(), "cli.chat.start",
		slog.Bool("mock", true),
		slog.Int("channel_count", len(cfg.DefaultChannels)),
	)
	if err := app.RunMockWithOptions(stdout, cfg, app.ClientOptions{DebugLogger: logger}); err != nil {
		logger.Log(context.Background(), "cli.chat.failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "mock chat: %v\n", err)
		return 1
	}
	logger.Log(context.Background(), "cli.chat.complete", slog.Bool("mock", true))
	return 0
}

// runLiveChatSession connects the shell to real Twitch chat: it loads stored
// credentials, validates the token and resolves the login it belongs to, then
// wires the transports and runs the UI.
func runLiveChatSession(cfg config.Config, stdout, stderr io.Writer) int {
	status, err := applyStoredCredentials(context.Background(), &cfg)
	if err != nil {
		fmt.Fprintf(stderr, "load credentials: %s\n", config.RedactDisplayValue(status.Err.Error()))
		return 1
	}
	logger, closeLog, ok := openDebugLoggerOrReport(cfg, stderr)
	if !ok {
		return 1
	}
	defer closeLog()
	logger.Log(context.Background(), "cli.chat.start",
		slog.Bool("mock", false),
		slog.Int("channel_count", len(cfg.DefaultChannels)),
	)
	if err := validateLiveChatConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	resolvedLogin, warning, err := validateLiveChatToken(context.Background(), cfg, newLiveTokenValidator())
	if err != nil {
		logger.Log(context.Background(), "cli.chat.token_validation_failed", slog.String("error", err.Error()))
		if hint := credentialPrecedenceHint(status); hint != "" {
			err = fmt.Errorf("%w %s", err, hint)
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if warning != "" {
		logger.Log(context.Background(), "cli.chat.token_validation_warning", slog.String("warning", warning))
		fmt.Fprintln(stderr, warning)
	}
	// The token owns the IRC identity; the config value is only a fallback for
	// when validation could not reach Twitch.
	if resolvedLogin != "" {
		cfg.Twitch.Username = resolvedLogin
	}
	if strings.TrimSpace(cfg.Twitch.Username) == "" {
		fmt.Fprintln(stderr, "could not determine the Twitch login for this token; run `twi doctor`, or set TWI_TWITCH_USERNAME to the account the token belongs to")
		return 2
	}
	if notice := refreshCapabilityWarning(cfg.Twitch); notice != "" {
		logger.Log(context.Background(), "cli.chat.refresh_unavailable")
		fmt.Fprintln(stderr, notice)
	}

	// One holder is shared by the IRC transport and every Helix client, so a
	// refresh reaches all of them rather than only the connection that
	// performed it.
	holder := newCredentialHolder(cfg.Twitch)
	client, err := newLiveChatClient(context.Background(), cfg, holder, logger, status)
	if err != nil {
		logger.Log(context.Background(), "cli.chat.failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "start Twitch IRC chat: %v\n", err)
		return 1
	}
	if err := runLiveChat(stdout, cfg, client, withDebugLogger(newLiveClientOptionsWithHolder(cfg, holder), logger)); err != nil {
		logger.Log(context.Background(), "cli.chat.failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "live chat: %v\n", err)
		return 1
	}
	logger.Log(context.Background(), "cli.chat.complete", slog.Bool("mock", false))
	return 0
}

// validateLiveChatConfig checks the credentials live chat cannot start
// without. A username is deliberately not one of them: the IRC login is
// derived from whoever the OAuth token belongs to (see validateLiveChatToken),
// so configuring it is optional and only ever used as a fallback when token
// validation is unreachable.
func validateLiveChatConfig(cfg config.Config) error {
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		return fmt.Errorf("missing Twitch credentials: set %s for live chat, or run `twi chat --mock`; OAuth token must include chat:read and chat:edit", "TWI_TWITCH_OAUTH_TOKEN or TWITCH_ACCESS_TOKEN")
	}
	return nil
}

// validateLiveChatToken validates the configured OAuth token and returns the
// Twitch login that IRC should authenticate as.
//
// Twitch requires the IRC NICK to be the account the token was issued to, and
// the token is the only authoritative source of that. So the validated login
// wins over any configured twitch_username: honoring a stale config value
// here would guarantee Twitch rejects the connection. A configured value that
// disagrees is reported as a warning, not an error - it is a stale setting,
// not a reason to refuse to start.
//
// The channel being joined is entirely separate: any account can read and
// send in any channel it is not banned from.
func validateLiveChatToken(ctx context.Context, cfg config.Config, validator twitch.TokenValidator) (login string, warning string, err error) {
	if validator == nil {
		return "", "warning: Twitch OAuth token validation is unavailable; continuing to IRC authentication. Run `twi doctor` to verify token identity, expiry, and scopes.", nil
	}

	credentials := twitch.TokenCredentials{
		Username:     cfg.Twitch.Username,
		OAuthToken:   cfg.Twitch.OAuthToken,
		RefreshToken: cfg.Twitch.RefreshToken,
		ClientID:     cfg.Twitch.ClientID,
		ClientSecret: cfg.Twitch.ClientSecret,
	}
	validation, err := validator.ValidateToken(ctx, credentials)
	redactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
	)
	if err != nil {
		detail := config.RedactDisplayValue(redactor.Redact(err.Error()))
		return "", "warning: Twitch OAuth token validation failed (" + detail + "); continuing to IRC authentication. Run `twi doctor` to verify token identity, expiry, and scopes.", nil
	}

	resolved := strings.TrimSpace(validation.Identity.Login)
	if stale := staleUsernameWarning(cfg.Twitch.Username, resolved); stale != "" {
		warning = stale
	}

	missing := validation.MissingRequiredIRCScopes()
	if len(missing) > 0 {
		return "", "", liveTokenValidationError(redactor, "missing required scopes: "+strings.Join(auth.ScopeValues(missing), ", "))
	}

	switch validation.Status {
	case twitch.TokenValidationValid, twitch.TokenValidationWrongUser:
		// WrongUser only means the configured username disagrees with the
		// token. The token is authoritative, so this is survivable.
		return resolved, warning, nil
	case twitch.TokenValidationMalformed:
		return "", "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "malformed OAuth token"))
	case twitch.TokenValidationExpired:
		return "", "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "OAuth token expired"))
	case twitch.TokenValidationMissingScope:
		return "", "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "missing required IRC scope"))
	default:
		return "", "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "token validation returned unknown state"))
	}
}

// staleUsernameWarning reports a configured twitch_username that disagrees
// with the token's owner. twi uses the token's login regardless; the warning
// exists so the stale setting is visible rather than silently overridden.
func staleUsernameWarning(configured, resolved string) string {
	if !twitch.LoginMismatch(configured, resolved) {
		return ""
	}
	return fmt.Sprintf(
		"warning: configured twitch_username %q is not the OAuth token's account; connecting as %q instead. Update or remove twitch_username to silence this.",
		configured, resolved,
	)
}

func liveTokenValidationError(redactor auth.Redactor, detail string) error {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "token is not valid for live IRC chat"
	}
	detail = config.RedactDisplayValue(redactor.Redact(detail))
	return fmt.Errorf("twitch OAuth token validation failed: %s. Run `twi doctor`; live chat requires chat:read and chat:edit scopes, matching username, and an unexpired token. Use `twi chat --mock` for credential-free mode", detail)
}

func liveTokenValidationDetail(validation twitch.TokenValidationResult, fallback string) string {
	if detail := strings.TrimSpace(validation.Detail); detail != "" {
		return detail
	}
	return fallback
}

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: twi config show|path")
		return 2
	}

	switch args[0] {
	case "path":
		path, err := config.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "config path: %s\n", config.RedactDisplayValue(err.Error()))
			return 1
		}
		fmt.Fprintln(stdout, config.RedactDisplayValue(path))
		return 0
	case "show":
		fs := flag.NewFlagSet("config show", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var cfgPath string
		fs.StringVar(&cfgPath, "config", "", "config file path")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		cfg, _, err := loadConfigWithStoredCredentials(context.Background(), os.Environ(), config.Overrides{ConfigPath: cfgPath})
		if err != nil {
			fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
			return 1
		}
		fmt.Fprint(stdout, cfg.RedactedString())
		return 0
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n", args[0])
		return 2
	}
}

// runProfile implements `twi profile list|show|set <name>`, the theme
// management surface documented alongside the Ctrl+T settings view.
func runProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: twi profile list|show|set <name>")
		return 2
	}

	switch args[0] {
	case "list":
		return runProfileList(args[1:], stdout, stderr)
	case "show":
		return runProfileShow(args[1:], stdout, stderr)
	case "set":
		return runProfileSet(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown profile command %q\n", args[0])
		return 2
	}
}

func runProfileList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("profile list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfgPath string
	fs.StringVar(&cfgPath, "config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(os.Environ(), config.Overrides{ConfigPath: cfgPath})
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	active := strings.ToLower(strings.TrimSpace(cfg.Features.ThemeName))
	for _, name := range append(theme.PresetNames(), "custom") {
		marker := "  "
		if name == active {
			marker = "> "
		}
		fmt.Fprintf(stdout, "%s%s\n", marker, name)
	}
	return 0
}

func runProfileShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfgPath string
	fs.StringVar(&cfgPath, "config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(os.Environ(), config.Overrides{ConfigPath: cfgPath})
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	palette := cfg.ResolveTheme()
	fmt.Fprintf(stdout, "theme_name = %s\n", cfg.Features.ThemeName)
	fmt.Fprintf(stdout, "background = %s\n", palette.Background)
	fmt.Fprintf(stdout, "foreground = %s\n", palette.Foreground)
	fmt.Fprintf(stdout, "accent = %s\n", palette.Accent)
	fmt.Fprintf(stdout, "muted = %s\n", palette.Muted)
	fmt.Fprintf(stdout, "border = %s\n", palette.Border)
	fmt.Fprintf(stdout, "surface = %s\n", palette.Surface)
	fmt.Fprintf(stdout, "warning = %s\n", palette.Warning)
	fmt.Fprintf(stdout, "error = %s\n", palette.Error)
	fmt.Fprintf(stdout, "success = %s\n", palette.Success)
	return 0
}

func runProfileSet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: twi profile set <name> [--background '#rrggbb' --foreground '#rrggbb' --accent '#rrggbb' --muted '#rrggbb' --border '#rrggbb' --surface '#rrggbb' --warning '#rrggbb' --error '#rrggbb' --success '#rrggbb']")
		return 2
	}
	name := strings.ToLower(strings.TrimSpace(args[0]))

	fs := flag.NewFlagSet("profile set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfgPath string
	var background, foreground, accent, muted, border, surface, warning, errorColor, success string
	fs.StringVar(&cfgPath, "config", "", "config file path")
	fs.StringVar(&background, "background", "", "custom theme background hex (only used with the 'custom' profile)")
	fs.StringVar(&foreground, "foreground", "", "custom theme foreground hex")
	fs.StringVar(&accent, "accent", "", "custom theme accent hex")
	fs.StringVar(&muted, "muted", "", "custom theme muted hex")
	fs.StringVar(&border, "border", "", "custom theme border hex")
	fs.StringVar(&surface, "surface", "", "custom theme surface hex")
	fs.StringVar(&warning, "warning", "", "custom theme warning hex")
	fs.StringVar(&errorColor, "error", "", "custom theme error hex")
	fs.StringVar(&success, "success", "", "custom theme success hex")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if name != "custom" {
		if _, ok := theme.Presets()[name]; !ok {
			fmt.Fprintf(stderr, "unknown theme %q; run `twi profile list` for available names\n", name)
			return 2
		}
	}

	cfg, err := config.Load(os.Environ(), config.Overrides{ConfigPath: cfgPath})
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	cfg.Features.ThemeName = name
	if name == "custom" {
		setIfNonEmpty(&cfg.Features.ThemeCustom.Background, background)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Foreground, foreground)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Accent, accent)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Muted, muted)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Border, border)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Surface, surface)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Warning, warning)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Error, errorColor)
		setIfNonEmpty(&cfg.Features.ThemeCustom.Success, success)
	}

	if err := config.WriteNonSecretFile(cfg.Path, cfg); err != nil {
		fmt.Fprintf(stderr, "save theme: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "theme set to %s\n", name)
	return 0
}

func setIfNonEmpty(dst *string, value string) {
	if strings.TrimSpace(value) != "" {
		*dst = value
	}
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfgPath string
	var debugFlags debugFlagOptions
	fs.StringVar(&cfgPath, "config", "", "config file path")
	addDebugFlags(fs, &debugFlags)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	environ := os.Environ()
	overrides := config.Overrides{ConfigPath: cfgPath}
	applyDebugFlagOverrides(&overrides, debugFlags)
	cfg, loadErr := config.Load(environ, overrides)
	if loadErr != nil {
		fallback, err := config.LoadEnvOnly(environ, overrides)
		if err != nil {
			fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
			return 1
		}
		cfg = fallback
	}
	credentialStatus, credentialErr := applyStoredCredentials(context.Background(), &cfg)
	if credentialErr != nil {
		credentialStatus.Err = credentialErr
	}
	logger, closeLog, ok := openDebugLoggerOrReport(cfg, stderr)
	if !ok {
		return 1
	}
	defer closeLog()
	logger.Log(context.Background(), "cli.doctor.start")

	report := buildDoctorReport(context.Background(), cfg, loadErr)
	if credentialStatus.Path != "" || credentialStatus.Label != "" || credentialStatus.Location != "" || credentialStatus.Present || credentialStatus.Err != nil {
		check := credentialFileDoctorCheck(credentialStatus)
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	logger.Log(context.Background(), "cli.doctor.complete", slog.Int("check_count", len(report.Checks)))
	return 0
}

func loadConfigWithStoredCredentials(ctx context.Context, environ []string, overrides config.Overrides) (config.Config, credentialLoadStatus, error) {
	cfg, err := config.Load(environ, overrides)
	if err != nil {
		return cfg, credentialLoadStatus{}, err
	}
	status, err := applyStoredCredentials(ctx, &cfg)
	return cfg, status, err
}

func applyStoredCredentials(ctx context.Context, cfg *config.Config) (credentialLoadStatus, error) {
	store, err := newCredentialStore()
	status := credentialLoadStatus{}
	if store != nil {
		status.Path = credentialStorePath(store)
		status.Label = credentialStoreLabel(store)
		status.Location = credentialStoreLocation(store)
		status.Store = store
	}
	if err != nil {
		status.Err = err
		if errors.Is(err, storage.ErrUnsupportedCredentialFilePlatform) {
			return status, nil
		}
		return status, err
	}
	if store == nil {
		return status, nil
	}

	record, ok, err := store.LoadCredentials(ctx)
	if err != nil {
		status.Err = err
		if errors.Is(err, storage.ErrUnsupportedCredentialFilePlatform) {
			return status, nil
		}
		return status, err
	}
	status.Present = ok
	if ok {
		status.Record = record.Clone()
		status.AccessTokenShadowed = record.AccessToken.Present() && strings.TrimSpace(cfg.Twitch.OAuthToken) != ""
		applyCredentialRecord(cfg, record)
	}
	return status, nil
}

// refreshCapabilityWarning names the gap that ends a long session.
//
// Twitch access tokens last about four hours. twi refreshes on auth failure,
// but the refresh flow needs the client secret alongside the refresh token and
// client ID, and `twi login` deliberately does not save a secret. The usual
// setup therefore holds a refresh token it can never redeem: chat simply stops
// partway through a stream, having warned about nothing. Saying so at startup
// costs one line and turns a mystery disconnect into a known limitation.
func refreshCapabilityWarning(cfg config.TwitchConfig) string {
	if strings.TrimSpace(cfg.RefreshToken) == "" || strings.TrimSpace(cfg.ClientSecret) != "" {
		return ""
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return ""
	}
	return "warning: no client secret is configured, so the saved refresh token cannot be redeemed. " +
		"Live chat will disconnect when this token expires (about 4 hours) and will not reconnect on its own. " +
		"Set TWI_TWITCH_CLIENT_SECRET to your Twitch application's secret, or re-run `twi login` if chat drops."
}

func credentialPrecedenceHint(status credentialLoadStatus) string {
	if !status.AccessTokenShadowed {
		return ""
	}
	return "Saved login credentials are present but were not used because environment or flat-config token credentials take precedence; unset TWI_TWITCH_OAUTH_TOKEN/TWITCH_ACCESS_TOKEN or remove twitch_oauth_token from config.toml, then retry."
}

func credentialStorePath(store storage.CredentialStore) string {
	if withPath, ok := store.(interface{ Path() string }); ok {
		return withPath.Path()
	}
	return ""
}

func credentialStoreLabel(store storage.CredentialStore) string {
	if withLabel, ok := store.(interface{ StoreLabel() string }); ok {
		return strings.TrimSpace(withLabel.StoreLabel())
	}
	if credentialStorePath(store) != "" {
		return "credential file"
	}
	return "credential store"
}

func credentialStoreLocation(store storage.CredentialStore) string {
	if withLocation, ok := store.(interface{ StoreLocation() string }); ok {
		return strings.TrimSpace(withLocation.StoreLocation())
	}
	if path := credentialStorePath(store); path != "" {
		return path
	}
	return credentialStoreLabel(store)
}

func applyCredentialRecord(cfg *config.Config, record storage.CredentialRecord) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.Twitch.Username) == "" {
		cfg.Twitch.Username = strings.TrimSpace(record.Login)
	}
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		cfg.Twitch.OAuthToken = twitch.NormalizeIRCOAuthToken(record.AccessToken.Reveal())
	}
	if strings.TrimSpace(cfg.Twitch.RefreshToken) == "" {
		cfg.Twitch.RefreshToken = record.RefreshToken.Reveal()
	}
	if strings.TrimSpace(cfg.Twitch.ClientID) == "" {
		cfg.Twitch.ClientID = strings.TrimSpace(record.ClientID)
	}
}

func persistRefreshedIRCCredentials(ctx context.Context, cfg config.Config, status credentialLoadStatus, refreshed irc.OAuthRefresh) error {
	redactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
		status.Record.AccessToken,
		status.Record.RefreshToken,
		refreshed.AccessToken,
		refreshed.RefreshToken,
	)
	if status.Store == nil {
		if status.Err != nil {
			return fmt.Errorf("credential store unavailable: %s", redactor.Redact(status.Err.Error()))
		}
		return errors.New("credential store unavailable")
	}

	record := refreshedCredentialRecord(cfg, status.Record, refreshed)
	if err := status.Store.SaveCredentials(ctx, record); err != nil {
		return fmt.Errorf("save refreshed credentials: %s", redactor.Redact(err.Error()))
	}
	return nil
}

func refreshedCredentialRecord(cfg config.Config, base storage.CredentialRecord, refreshed irc.OAuthRefresh) storage.CredentialRecord {
	record := base.Clone()
	if login := strings.TrimSpace(cfg.Twitch.Username); login != "" {
		if record.Login != "" && !strings.EqualFold(record.Login, login) {
			record.UserID = ""
			record.DisplayName = ""
		}
		record.Login = login
	}
	if clientID := strings.TrimSpace(cfg.Twitch.ClientID); clientID != "" {
		record.ClientID = clientID
	}
	record.AccessToken = refreshed.AccessToken
	record.RefreshToken = refreshed.RefreshToken
	if strings.TrimSpace(refreshed.TokenType) != "" {
		record.TokenType = strings.TrimSpace(refreshed.TokenType)
	} else if strings.TrimSpace(record.TokenType) == "" {
		record.TokenType = "bearer"
	}
	if len(refreshed.Scopes) > 0 {
		record.Scopes = append([]auth.Scope(nil), refreshed.Scopes...)
	} else if len(record.Scopes) == 0 {
		record.Scopes = auth.LoginScopes()
	}
	if !refreshed.ExpiresAt.IsZero() {
		record.ExpiresAt = refreshed.ExpiresAt
	}
	if !refreshed.RefreshedAt.IsZero() {
		record.UpdatedAt = refreshed.RefreshedAt.UTC()
	} else {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func credentialFileDoctorCheck(status credentialLoadStatus) doctor.Check {
	label := strings.TrimSpace(status.Label)
	if label == "" {
		label = "credential store"
	}
	location := strings.TrimSpace(status.Location)
	if location == "" {
		location = strings.TrimSpace(status.Path)
	}
	if location == "" {
		location = label
	}
	displayLocation := config.RedactDisplayValue(location)
	if status.Err != nil {
		return doctor.Check{
			Name:   label,
			Status: doctor.StatusWarn,
			Detail: fmt.Sprintf("%s load failed: %s; using env/config/defaults", displayLocation, config.RedactDisplayValue(status.Err.Error())),
		}
	}
	if status.Present {
		return doctor.Check{
			Name:   label,
			Status: doctor.StatusOK,
			Detail: displayLocation + " loaded",
		}
	}
	return doctor.Check{
		Name:   label,
		Status: doctor.StatusWarn,
		Detail: displayLocation + " not found; run `twi login` after configuring a Twitch app client",
	}
}

type channelFlags []string

func (f *channelFlags) String() string {
	return strings.Join(*f, ",")
}

// Set accepts either a single channel or a comma-separated list, so
// --channel and --channels can share one accumulating flag value. Both may
// be repeated, and both append rather than replace.
func (f *channelFlags) Set(value string) error {
	added := false
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "#"))
		if part == "" {
			continue
		}
		*f = append(*f, part)
		added = true
	}
	if !added {
		return errors.New("channel cannot be empty")
	}
	return nil
}

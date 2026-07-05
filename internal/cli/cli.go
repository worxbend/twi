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
	"path/filepath"
	"strings"
	"time"

	"github.com/w0rxbend/twi/internal/app"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/debuglog"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

const usage = `twi is a terminal Twitch chat client.

Usage:
  twi chat [--channel name] [--mock] [--debug-log]
  twi config show
  twi config path
  twi doctor
  twi login [--dry-run]
  twi setup

Environment:
  TWI_TWITCH_USERNAME
  TWI_TWITCH_OAUTH_TOKEN
  TWI_TWITCH_REFRESH_TOKEN
  TWI_TWITCH_CLIENT_ID
  TWI_TWITCH_CLIENT_SECRET
  TWITCH_USERNAME
  TWITCH_ACCESS_TOKEN
  TWITCH_REFRESH_TOKEN
  TWITCH_CLIENT_ID
  TWITCH_CLIENT_SECRET
  TWI_DEFAULT_CHANNELS
  TWI_ENABLE_KITTY_IMAGES
  TWI_ENABLE_MOUSE
  TWI_IMAGE_MODE
  TWI_AVATAR_MODE
  TWI_EMOJI_MODE
  TWI_EMOJI_PROVIDER
  TWI_EMOJI_URL_TEMPLATE
  TWI_EMOTE_MODE
  TWI_ANIMATION_MODE
  TWI_DEBUG_LOG
  TWI_DEBUG_LOG_PATH
`

var newLiveChatClient = func(ctx context.Context, cfg config.Config, logger debuglog.Logger, credentialStatus credentialLoadStatus) (app.ChatClient, error) {
	return app.NewRestartableLiveChatClientWithOptions(ctx, liveIRCTransportFactory(cfg, logger, credentialStatus), 0, app.LiveChatClientOptions{
		DebugLogger: logger,
	})
}

func liveIRCTransportFactory(cfg config.Config, logger debuglog.Logger, credentialStatus credentialLoadStatus) app.LiveChatTransportFactory {
	return func(context.Context) (twitch.ChatClient, error) {
		return twitch.NewIRCClient(twitch.IRCConfig{
			Username:     cfg.Twitch.Username,
			OAuthToken:   cfg.Twitch.OAuthToken,
			RefreshToken: cfg.Twitch.RefreshToken,
			ClientID:     cfg.Twitch.ClientID,
			ClientSecret: cfg.Twitch.ClientSecret,
			Channels:     cfg.DefaultChannels,
			DebugLogger:  logger,
			OnOAuthRefresh: func(ctx context.Context, refreshed twitch.OAuthRefresh) error {
				return persistRefreshedIRCCredentials(ctx, cfg, credentialStatus, refreshed)
			},
		})
	}
}

var newLiveClientOptions = func(cfg config.Config) app.ClientOptions {
	return liveClientOptions(cfg, os.Environ(), "")
}

var runLiveChat = app.RunClientWithOptions

var newDoctorTokenValidator = func() twitch.TokenValidator {
	return twitch.NewOAuthTokenValidator(twitch.OAuthTokenValidatorConfig{
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
}

var newLiveTokenValidator = func() twitch.TokenValidator {
	return newDoctorTokenValidator()
}

var doctorReachabilityProbe = app.ProbeTwitchIRCReachability

var doctorCacheDir = func() string {
	return ""
}

var buildDoctorReport = func(ctx context.Context, cfg config.Config, cfgErr error) app.DoctorReport {
	return app.DoctorWithOptions(ctx, cfg, app.DoctorOptions{
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
	Path     string
	Label    string
	Location string
	Present  bool
	Err      error
	Store    storage.CredentialStore
	Record   storage.CredentialRecord
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
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runChat(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var channels channelFlags
	var cfgPath string
	var mock bool
	var debugFlags debugFlagOptions
	fs.Var(&channels, "channel", "Twitch channel to join; repeat for multiple channels")
	fs.StringVar(&cfgPath, "config", "", "config file path")
	fs.BoolVar(&mock, "mock", false, "run against the built-in mock chat source")
	addDebugFlags(fs, &debugFlags)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	overrides := config.Overrides{
		ConfigPath: cfgPath,
		Channels:   []string(channels),
	}
	applyDebugFlagOverrides(&overrides, debugFlags)
	cfg, err := config.Load(os.Environ(), overrides)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	if len(channels) > 0 {
		cfg.DefaultChannels = []string(channels)
	}

	if mock {
		logger, closeLog, err := openDebugLogger(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "open debug log: %s\n", config.RedactDisplayValue(err.Error()))
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

	status, err := applyStoredCredentials(context.Background(), &cfg)
	if err != nil {
		fmt.Fprintf(stderr, "load credentials: %s\n", config.RedactDisplayValue(status.Err.Error()))
		return 1
	}
	logger, closeLog, err := openDebugLogger(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "open debug log: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	defer closeLog()
	logger.Log(context.Background(), "cli.chat.start",
		slog.Bool("mock", false),
		slog.Int("channel_count", len(cfg.DefaultChannels)),
	)
	if len(cfg.DefaultChannels) == 0 {
		fmt.Fprintln(stderr, "no channel configured; pass --channel or set TWI_DEFAULT_CHANNELS")
		return 2
	}
	if err := validateLiveChatConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if warning, err := validateLiveChatToken(context.Background(), cfg, newLiveTokenValidator()); err != nil {
		logger.Log(context.Background(), "cli.chat.token_validation_failed", slog.String("error", err.Error()))
		fmt.Fprintln(stderr, err)
		return 2
	} else if warning != "" {
		logger.Log(context.Background(), "cli.chat.token_validation_warning", slog.String("warning", warning))
		fmt.Fprintln(stderr, warning)
	}

	client, err := newLiveChatClient(context.Background(), cfg, logger, status)
	if err != nil {
		logger.Log(context.Background(), "cli.chat.failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "start Twitch IRC chat: %v\n", err)
		return 1
	}
	if err := runLiveChat(stdout, cfg, client, withDebugLogger(newLiveClientOptions(cfg), logger)); err != nil {
		logger.Log(context.Background(), "cli.chat.failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "live chat: %v\n", err)
		return 1
	}
	logger.Log(context.Background(), "cli.chat.complete", slog.Bool("mock", false))
	return 0
}

func liveClientOptions(cfg config.Config, environ []string, cacheDir string) app.ClientOptions {
	decision := app.DecideLiveImageStack(cfg, environ, cacheDir)
	if !decision.Ready {
		return app.ClientOptions{}
	}

	cache := storage.NewDiskAssetCache(decision.CacheDir)
	helixHTTPClient := &http.Client{Timeout: 2 * time.Second}
	downloadHTTPClient := &http.Client{Timeout: 4 * time.Second}

	var userLookup assets.AvatarLookup
	var identityLookup assets.IdentityLookup
	if decision.Supports(assets.KindAvatar) {
		userLookup = twitch.NewHelixUsersClient(twitch.HelixUsersClientConfig{
			HTTPClient: helixHTTPClient,
			ClientID:   cfg.Twitch.ClientID,
			OAuthToken: cfg.Twitch.OAuthToken,
		})
		identityLookup = liveIdentityLookup{lookup: userLookup}
	}

	var twitchMetadata assets.MetadataLookup
	if decision.Supports(assets.KindBadge) || decision.Supports(assets.KindTwitchEmote) {
		twitchMetadata = &assets.TwitchMetadataResolver{
			Lookup: twitch.NewHelixChatAssetsClient(twitch.HelixChatAssetsClientConfig{
				HTTPClient: helixHTTPClient,
				ClientID:   cfg.Twitch.ClientID,
				OAuthToken: cfg.Twitch.OAuthToken,
			}),
			Cache: cache,
		}
	}

	var emojiMetadata assets.MetadataLookup
	if decision.Supports(assets.KindEmoji) {
		emojiMetadata = assets.NewEmojiMetadataProvider(assets.EmojiProviderConfig{
			Provider:    cfg.Features.EmojiProvider,
			URLTemplate: cfg.Features.EmojiURLTemplate,
			Cache:       cache,
		})
	}

	opts := app.ClientOptions{
		AssetResolver: &assets.Resolver{
			Identity: identityLookup,
			Metadata: liveMetadataLookup{
				twitch: twitchMetadata,
				emoji:  emojiMetadata,
			},
			Downloader: assets.NewPublicImageDownloader(assets.PublicImageDownloaderOptions{
				HTTPClient:  downloadHTTPClient,
				DownloadDir: filepath.Join(decision.CacheDir, "downloads"),
			}),
			Cache: cache,
		},
		AssetKinds:    decision.SupportedKindSet(),
		ImagePreparer: render.NewPNGImagePreparer(render.ImagePrepareOptions{PreparedCache: cache}),
		ImageRenderer: render.NewKittyRenderer(decision.Capability),
	}
	if userLookup != nil {
		opts.AvatarResolver = &assets.AvatarBatchResolver{
			Lookup: userLookup,
			Cache:  cache,
		}
	}
	return opts
}

type liveIdentityLookup struct {
	lookup assets.AvatarLookup
}

func (l liveIdentityLookup) LookupIdentity(ctx context.Context, req assets.IdentityRequest) (assets.Identity, error) {
	if l.lookup == nil {
		return assets.Identity{}, nil
	}
	users, err := l.lookup.GetUsers(ctx, twitch.UserLookupRequest{
		UserIDs:    []string{req.UserID},
		UserLogins: []string{req.UserLogin},
	})
	if err != nil || len(users) == 0 {
		return assets.Identity{}, err
	}
	user := users[0]
	return assets.Identity{
		UserID:      user.UserID,
		Login:       user.Login,
		DisplayName: user.DisplayName,
		AvatarURL:   user.ProfileImageURL,
	}, nil
}

type liveMetadataLookup struct {
	twitch assets.MetadataLookup
	emoji  assets.MetadataLookup
}

func (l liveMetadataLookup) LookupMetadata(ctx context.Context, req assets.MetadataRequest) (assets.Metadata, error) {
	switch req.Ref.Kind {
	case assets.KindBadge, assets.KindTwitchEmote:
		if l.twitch != nil {
			return l.twitch.LookupMetadata(ctx, req)
		}
	case assets.KindEmoji:
		if l.emoji != nil {
			return l.emoji.LookupMetadata(ctx, req)
		}
	}
	return assets.Metadata{
		Ref:         req.Ref,
		URL:         req.Ref.URL,
		WidthCells:  0,
		HeightCells: 0,
	}, nil
}

func validateLiveChatConfig(cfg config.Config) error {
	var missing []string
	if strings.TrimSpace(cfg.Twitch.Username) == "" {
		missing = append(missing, "TWI_TWITCH_USERNAME or TWITCH_USERNAME")
	}
	if strings.TrimSpace(cfg.Twitch.OAuthToken) == "" {
		missing = append(missing, "TWI_TWITCH_OAUTH_TOKEN or TWITCH_ACCESS_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing Twitch credentials: set %s for live chat, or run `twi chat --mock`; OAuth token must include chat:read and chat:edit", strings.Join(missing, " and "))
	}
	return nil
}

func validateLiveChatToken(ctx context.Context, cfg config.Config, validator twitch.TokenValidator) (string, error) {
	if validator == nil {
		return "warning: Twitch OAuth token validation is unavailable; continuing to IRC authentication. Run `twi doctor` to verify token identity, expiry, and scopes.", nil
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
		return "warning: Twitch OAuth token validation failed (" + detail + "); continuing to IRC authentication. Run `twi doctor` to verify token identity, expiry, and scopes.", nil
	}

	mismatch := liveTokenUsernameMismatch(cfg.Twitch.Username, validation.Identity.Login)
	if validation.Status == twitch.TokenValidationWrongUser {
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, mismatch))
	}
	if validation.Status == twitch.TokenValidationValid && mismatch != "" {
		return "", liveTokenValidationError(redactor, mismatch)
	}

	missing := validation.MissingScopes
	if len(missing) == 0 {
		missing = twitch.MissingRequiredIRCScopes(validation.Scopes)
	}
	if validation.Status == twitch.TokenValidationValid && len(missing) == 0 {
		return "", nil
	}
	if len(missing) > 0 {
		return "", liveTokenValidationError(redactor, "missing required scopes: "+strings.Join(auth.ScopeValues(missing), ", "))
	}

	switch validation.Status {
	case twitch.TokenValidationMalformed:
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "malformed OAuth token"))
	case twitch.TokenValidationExpired:
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "OAuth token expired"))
	case twitch.TokenValidationWrongUser:
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, mismatch))
	case twitch.TokenValidationMissingScope:
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "missing required IRC scope"))
	default:
		return "", liveTokenValidationError(redactor, liveTokenValidationDetail(validation, "token validation returned unknown state"))
	}
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

func liveTokenUsernameMismatch(configured, actual string) string {
	configured = strings.TrimSpace(configured)
	actual = strings.TrimSpace(actual)
	if configured == "" || actual == "" || strings.EqualFold(configured, actual) {
		return ""
	}
	return fmt.Sprintf("configured username %q does not match token identity %q", configured, actual)
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
	logger, closeLog, err := openDebugLogger(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "open debug log: %s\n", config.RedactDisplayValue(err.Error()))
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
		applyCredentialRecord(cfg, record)
	}
	return status, nil
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
		cfg.Twitch.OAuthToken = normalizeIRCOAuthToken(record.AccessToken.Reveal())
	}
	if strings.TrimSpace(cfg.Twitch.RefreshToken) == "" {
		cfg.Twitch.RefreshToken = record.RefreshToken.Reveal()
	}
	if strings.TrimSpace(cfg.Twitch.ClientID) == "" {
		cfg.Twitch.ClientID = strings.TrimSpace(record.ClientID)
	}
}

func persistRefreshedIRCCredentials(ctx context.Context, cfg config.Config, status credentialLoadStatus, refreshed twitch.OAuthRefresh) error {
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

func refreshedCredentialRecord(cfg config.Config, base storage.CredentialRecord, refreshed twitch.OAuthRefresh) storage.CredentialRecord {
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
		record.Scopes = auth.RequiredChatScopes()
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

func normalizeIRCOAuthToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(strings.ToLower(value), "oauth:") {
		return value
	}
	return "oauth:" + value
}

func credentialFileDoctorCheck(status credentialLoadStatus) app.DoctorCheck {
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
		return app.DoctorCheck{
			Name:   label,
			Status: app.DoctorStatusWarn,
			Detail: fmt.Sprintf("%s load failed: %s; using env/config/defaults", displayLocation, config.RedactDisplayValue(status.Err.Error())),
		}
	}
	if status.Present {
		return app.DoctorCheck{
			Name:   label,
			Status: app.DoctorStatusOK,
			Detail: displayLocation + " loaded",
		}
	}
	return app.DoctorCheck{
		Name:   label,
		Status: app.DoctorStatusWarn,
		Detail: displayLocation + " not found; run `twi login` after configuring a Twitch app client",
	}
}

type channelFlags []string

func (f *channelFlags) String() string {
	return strings.Join(*f, ",")
}

func (f *channelFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("channel cannot be empty")
	}
	*f = append(*f, strings.TrimPrefix(value, "#"))
	return nil
}

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/w0rxbend/twi/internal/app"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/render"
	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

const usage = `twi is a terminal Twitch chat client.

Usage:
  twi chat [--channel name] [--mock]
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
`

var newLiveChatClient = func(ctx context.Context, cfg config.Config) (app.ChatClient, error) {
	return app.NewRestartableLiveChatClient(ctx, liveIRCTransportFactory(cfg), 0)
}

func liveIRCTransportFactory(cfg config.Config) app.LiveChatTransportFactory {
	return func(context.Context) (twitch.ChatClient, error) {
		return twitch.NewIRCClient(twitch.IRCConfig{
			Username:     cfg.Twitch.Username,
			OAuthToken:   cfg.Twitch.OAuthToken,
			RefreshToken: cfg.Twitch.RefreshToken,
			ClientID:     cfg.Twitch.ClientID,
			ClientSecret: cfg.Twitch.ClientSecret,
			Channels:     cfg.DefaultChannels,
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
	return storage.NewDefaultCredentialFileStore()
}

type credentialLoadStatus struct {
	Path    string
	Present bool
	Err     error
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
	fs.Var(&channels, "channel", "Twitch channel to join; repeat for multiple channels")
	fs.StringVar(&cfgPath, "config", "", "config file path")
	fs.BoolVar(&mock, "mock", false, "run against the built-in mock chat source")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	overrides := config.Overrides{
		ConfigPath: cfgPath,
		Channels:   []string(channels),
	}
	cfg, err := config.Load(os.Environ(), overrides)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	if len(channels) > 0 {
		cfg.DefaultChannels = []string(channels)
	}

	if mock {
		if err := app.RunMock(stdout, cfg); err != nil {
			fmt.Fprintf(stderr, "mock chat: %v\n", err)
			return 1
		}
		return 0
	}

	status, err := applyStoredCredentials(context.Background(), &cfg)
	if err != nil {
		fmt.Fprintf(stderr, "load credentials: %s\n", config.RedactDisplayValue(status.Err.Error()))
		return 1
	}
	if len(cfg.DefaultChannels) == 0 {
		fmt.Fprintln(stderr, "no channel configured; pass --channel or set TWI_DEFAULT_CHANNELS")
		return 2
	}
	if err := validateLiveChatConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	client, err := newLiveChatClient(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "start Twitch IRC chat: %v\n", err)
		return 1
	}
	if err := runLiveChat(stdout, cfg, client, newLiveClientOptions(cfg)); err != nil {
		fmt.Fprintf(stderr, "live chat: %v\n", err)
		return 1
	}
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
	fs.StringVar(&cfgPath, "config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	environ := os.Environ()
	overrides := config.Overrides{ConfigPath: cfgPath}
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

	report := buildDoctorReport(context.Background(), cfg, loadErr)
	if credentialStatus.Path != "" || credentialStatus.Present || credentialStatus.Err != nil {
		check := credentialFileDoctorCheck(credentialStatus)
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
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

func normalizeIRCOAuthToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(strings.ToLower(value), "oauth:") {
		return value
	}
	return "oauth:" + value
}

func credentialFileDoctorCheck(status credentialLoadStatus) app.DoctorCheck {
	path := strings.TrimSpace(status.Path)
	if path == "" {
		path = "credential file"
	}
	displayPath := config.RedactDisplayValue(path)
	if status.Err != nil {
		return app.DoctorCheck{
			Name:   "credential file",
			Status: app.DoctorStatusWarn,
			Detail: fmt.Sprintf("%s load failed: %s; using env/config/defaults", displayPath, config.RedactDisplayValue(status.Err.Error())),
		}
	}
	if status.Present {
		return app.DoctorCheck{
			Name:   "credential file",
			Status: app.DoctorStatusOK,
			Detail: displayPath + " loaded",
		}
	}
	return app.DoctorCheck{
		Name:   "credential file",
		Status: app.DoctorStatusWarn,
		Detail: displayPath + " not found; run `twi login` after configuring a Twitch app client",
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

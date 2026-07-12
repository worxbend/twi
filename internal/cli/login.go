package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/storage"
)

const (
	defaultLoginRedirectURI = "http://127.0.0.1:17643/oauth/twitch/callback"
	defaultLoginTimeout     = 5 * time.Minute
)

const loginUsage = `Usage:
  twi login [--config path] [--redirect-uri url] [--timeout duration] [--dry-run] [--write-default-config]

Starts Twitch OAuth login for the MVP IRC chat scopes:
  chat:read  read Twitch IRC chat
  chat:edit  send Twitch IRC chat

Required for a real login:
  TWI_TWITCH_CLIENT_ID or TWITCH_CLIENT_ID
  TWI_TWITCH_CLIENT_SECRET or TWITCH_CLIENT_SECRET

Behavior:
  twi opens a browser, listens for the localhost OAuth callback, validates the
  returned token with Twitch, and prints only identity/scope status. Access
  tokens, refresh tokens, callback codes, OAuth state, authorization URLs, and
  client secrets are not printed. Successful logins save tokens through the
  private credential store on supported Unix platforms. Non-Unix builds keep
  saved credentials disabled, so use environment variables or a private flat
  config file there. Environment variables and flat config credentials still
  take precedence when present.

  The redirect URI defaults to --redirect-uri's flag default unless
  twitch_redirect_url (or TWI_TWITCH_REDIRECT_URL) is set in config, in which
  case that value is used instead; an explicit --redirect-uri flag always
  wins over both. --write-default-config writes a starter config.toml (with
  no secrets) at the effective config path first, but only if that file does
  not already exist, and never during --dry-run.

Flags:
`

type loginCallbackWaiter interface {
	Wait(context.Context, auth.Secret) (auth.LoginCallback, error)
	Close() error
}

var newLoginFlow = func() auth.LoginFlow {
	return auth.NewTwitchOAuthLoginFlow(auth.TwitchOAuthLoginFlowConfig{})
}

var newLoginContext = func(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

var newLoginCallbackWaiter = func(redirectURI string) (loginCallbackWaiter, error) {
	return newLocalLoginCallbackWaiter(redirectURI)
}

var openLoginBrowser = openBrowser

func runLogin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfgPath string
	var redirectURI string
	var timeout time.Duration
	var dryRun bool
	var writeDefaultConfig bool
	var debugFlags debugFlagOptions
	fs.StringVar(&cfgPath, "config", "", "config file path")
	fs.StringVar(&redirectURI, "redirect-uri", defaultLoginRedirectURI, "localhost OAuth callback URL registered for the Twitch app")
	fs.DurationVar(&timeout, "timeout", defaultLoginTimeout, "maximum time to wait for browser authorization and callback")
	fs.BoolVar(&dryRun, "dry-run", false, "explain login requirements without opening a browser, listening for a callback, or contacting Twitch")
	fs.BoolVar(&writeDefaultConfig, "write-default-config", false, "write a starter config.toml at the effective config path first, only if it does not already exist (skipped during --dry-run)")
	addDebugFlags(fs, &debugFlags)
	fs.Usage = func() {
		fmt.Fprint(stderr, loginUsage)
		fs.PrintDefaults()
	}

	if hasHelpArg(args) {
		fmt.Fprint(stdout, loginUsage)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		return 0
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected login argument %q\n\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	if timeout <= 0 {
		fmt.Fprintln(stderr, "login timeout must be greater than zero")
		return 2
	}
	redirectURIExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "redirect-uri" {
			redirectURIExplicit = true
		}
	})

	overrides := config.Overrides{ConfigPath: cfgPath}
	applyDebugFlagOverrides(&overrides, debugFlags)
	cfg, err := config.Load(os.Environ(), overrides)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	if !redirectURIExplicit {
		if configured := strings.TrimSpace(cfg.Twitch.RedirectURL); configured != "" {
			redirectURI = configured
		}
	}
	if writeDefaultConfig && !dryRun {
		wrote, err := writeDefaultConfigIfMissing(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "write default config: %s\n", config.RedactDisplayValue(err.Error()))
			return 1
		}
		if wrote {
			fmt.Fprintf(stdout, "Default config written to %s.\n", config.RedactDisplayValue(cfg.Path))
		} else {
			fmt.Fprintf(stdout, "Config already exists at %s; left unchanged.\n", config.RedactDisplayValue(cfg.Path))
		}
	}
	logger, closeLog, err := openDebugLogger(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "open debug log: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	defer closeLog()
	logger.Log(context.Background(), "cli.login.start",
		slog.Bool("dry_run", dryRun),
		slog.String("redirect_uri", redirectURI),
		slog.Int64("timeout_ms", int64(timeout/time.Millisecond)),
	)

	if dryRun {
		printLoginDryRun(stdout, cfg, redirectURI, timeout)
		logger.Log(context.Background(), "cli.login.complete", slog.Bool("dry_run", true))
		return 0
	}

	request := auth.LoginRequest{
		ClientID:     strings.TrimSpace(cfg.Twitch.ClientID),
		ClientSecret: auth.NewSecret(cfg.Twitch.ClientSecret),
		RedirectURI:  strings.TrimSpace(redirectURI),
		Scopes:       auth.RequiredChatScopes(),
	}
	baseRedactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
	)
	if err := validateLoginConfig(request); err != nil {
		logger.Log(context.Background(), "cli.login.config_invalid", slog.String("error", baseRedactor.Redact(err.Error())))
		fmt.Fprintln(stderr, baseRedactor.Redact(err.Error()))
		return 2
	}

	store, err := newCredentialStore()
	if err != nil {
		logger.Log(context.Background(), "cli.login.storage_failed", slog.String("error", baseRedactor.Redact(err.Error())))
		printLoginError(stderr, "prepare credential storage", err, baseRedactor)
		return 1
	}
	if store == nil {
		err := errors.New("credential store unavailable")
		logger.Log(context.Background(), "cli.login.storage_failed", slog.String("error", err.Error()))
		printLoginError(stderr, "prepare credential storage", err, baseRedactor)
		return 1
	}

	waiter, err := newLoginCallbackWaiter(request.RedirectURI)
	if err != nil {
		logger.Log(context.Background(), "cli.login.callback_unavailable", slog.String("error", baseRedactor.Redact(err.Error())))
		fmt.Fprintf(stderr, "login callback unavailable: %s\n", baseRedactor.Redact(err.Error()))
		return 2
	}
	defer waiter.Close()

	ctx, cancel := newLoginContext(timeout)
	defer cancel()

	flow := newLoginFlow()
	challenge, err := flow.BeginLogin(ctx, request)
	if err != nil {
		logger.Log(context.Background(), "cli.login.begin_failed", slog.String("error", baseRedactor.Redact(err.Error())))
		printLoginError(stderr, "start login", err, baseRedactor)
		return 1
	}
	logger = logger.WithSecrets(challenge.AuthorizationURL, challenge.State)
	logger.Log(context.Background(), "cli.login.begin_succeeded",
		slog.Int("scope_count", len(challenge.Scopes)),
		slog.Time("expires_at", challenge.ExpiresAt),
	)

	challengeRedactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
		challenge.AuthorizationURL,
		challenge.State,
	)

	fmt.Fprintf(stdout, "Starting Twitch OAuth login for scopes: %s\n", strings.Join(auth.ScopeValues(challenge.Scopes), ", "))
	fmt.Fprintln(stdout, "A browser window will open. Approve the requested scopes, then return to this terminal.")
	fmt.Fprintln(stdout, "Tokens will be validated, saved privately, and never printed.")

	if err := openLoginBrowser(ctx, challenge.AuthorizationURL.Reveal()); err != nil {
		logger.Log(context.Background(), "cli.login.browser_failed", slog.String("error", challengeRedactor.Redact(err.Error())))
		printLoginError(stderr, "open browser", err, challengeRedactor)
		return 1
	}
	logger.Log(context.Background(), "cli.login.browser_opened")

	callback, err := waiter.Wait(ctx, challenge.State)
	if err != nil {
		logger.Log(context.Background(), "cli.login.callback_failed", slog.String("error", challengeRedactor.Redact(err.Error())))
		printLoginError(stderr, "wait for OAuth callback", err, challengeRedactor)
		return 1
	}
	logger = logger.WithSecrets(callback.Code, callback.State, callback.ExpectedState)
	logger.Log(context.Background(), "cli.login.callback_received",
		slog.Bool("has_code", callback.Code.Present()),
		slog.Bool("state_present", callback.State.Present()),
	)

	callbackRedactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
		challenge.AuthorizationURL,
		challenge.State,
		callback.Code,
		callback.State,
		callback.ExpectedState,
	)
	result, err := flow.CompleteLogin(ctx, callback)
	if err != nil {
		logger.Log(context.Background(), "cli.login.complete_failed", slog.String("error", callbackRedactor.Redact(err.Error())))
		printLoginError(stderr, "complete login", err, callbackRedactor)
		return 1
	}
	logger = logger.WithSecrets(result.Tokens.AccessToken, result.Tokens.RefreshToken)
	logger.Log(context.Background(), "cli.login.complete_succeeded",
		slog.String("login", result.Identity.Login),
		slog.Int("scope_count", len(result.Scopes)),
		slog.Bool("refresh_available", result.Tokens.RefreshAvailable()),
	)

	resultRedactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
		challenge.AuthorizationURL,
		challenge.State,
		callback.Code,
		callback.State,
		callback.ExpectedState,
		result.Tokens.AccessToken,
		result.Tokens.RefreshToken,
	)
	record := storage.CredentialRecordFromLoginResult(result, request.ClientID, time.Now().UTC())
	if err := store.SaveCredentials(ctx, record); err != nil {
		logger.Log(context.Background(), "cli.login.save_failed", slog.String("error", resultRedactor.Redact(err.Error())))
		printLoginError(stderr, "save credentials", err, resultRedactor)
		return 1
	}
	logger.Log(context.Background(), "cli.login.save_succeeded")
	printLoginSuccess(stdout, result, resultRedactor)
	logger.Log(context.Background(), "cli.login.complete", slog.Bool("dry_run", false))
	return 0
}

// writeDefaultConfigIfMissing writes the effective non-secret config (built-in
// defaults merged with any already-present env vars) to cfg.Path, but only
// when no file exists there yet, so a user can inspect and edit a starter
// config.toml before authenticating. It never overwrites an existing file.
func writeDefaultConfigIfMissing(cfg config.Config) (wrote bool, err error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return false, errors.New("config path is empty")
	}
	switch _, statErr := os.Stat(cfg.Path); {
	case statErr == nil:
		return false, nil
	case !errors.Is(statErr, os.ErrNotExist):
		return false, statErr
	}
	if err := config.WriteNonSecretFile(cfg.Path, cfg); err != nil {
		return false, err
	}
	return true, nil
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			return true
		}
	}
	return false
}

func validateLoginConfig(request auth.LoginRequest) error {
	var missing []string
	if strings.TrimSpace(request.ClientID) == "" {
		missing = append(missing, "TWI_TWITCH_CLIENT_ID or TWITCH_CLIENT_ID")
	}
	if !request.ClientSecret.Present() {
		missing = append(missing, "TWI_TWITCH_CLIENT_SECRET or TWITCH_CLIENT_SECRET")
	}
	if strings.TrimSpace(request.RedirectURI) == "" {
		missing = append(missing, "--redirect-uri")
	}
	if len(missing) > 0 {
		return fmt.Errorf("login requires %s; existing env/config token credentials still work for `twi chat`, and saved credentials are used on supported Unix platforms when those sources are empty", strings.Join(missing, " and "))
	}
	return nil
}

func printLoginDryRun(stdout io.Writer, cfg config.Config, redirectURI string, timeout time.Duration) {
	redactor := auth.NewRedactor(
		auth.NewSecret(cfg.Twitch.OAuthToken),
		auth.NewSecret(cfg.Twitch.RefreshToken),
		auth.NewSecret(cfg.Twitch.ClientSecret),
	)

	fmt.Fprintln(stdout, "Twitch OAuth login dry run")
	fmt.Fprintf(stdout, "Requested scopes: %s\n", strings.Join(auth.ScopeValues(auth.RequiredChatScopes()), ", "))
	fmt.Fprintf(stdout, "Redirect URI: %s\n", redactor.Redact(redirectURI))
	fmt.Fprintf(stdout, "Timeout: %s\n", timeout)
	fmt.Fprintf(stdout, "Client ID: %s\n", presentMissing(cfg.Twitch.ClientID))
	fmt.Fprintf(stdout, "Client secret: %s\n", presentMissing(cfg.Twitch.ClientSecret))
	fmt.Fprintln(stdout, "Real login opens a browser and waits for a localhost callback.")
	fmt.Fprintln(stdout, "On supported Unix platforms, tokens are validated, saved privately, and never printed.")
	fmt.Fprintln(stdout, "For live chat, saved credentials are used when environment variables or flat config credentials are empty.")
}

func presentMissing(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "present"
}

func printLoginSuccess(stdout io.Writer, result auth.LoginResult, redactor auth.Redactor) {
	login := strings.TrimSpace(result.Identity.Login)
	if login == "" {
		login = strings.TrimSpace(result.Identity.DisplayName)
	}
	if login == "" {
		login = "unknown"
	}
	fmt.Fprintf(stdout, "Login succeeded for Twitch user: %s\n", redactor.Redact(login))
	fmt.Fprintf(stdout, "Granted scopes: %s\n", strings.Join(auth.ScopeValues(result.Scopes), ", "))
	if result.Tokens.RefreshAvailable() {
		fmt.Fprintln(stdout, "Refresh token: received")
	} else {
		fmt.Fprintln(stdout, "Refresh token: not returned")
	}
	fmt.Fprintln(stdout, "Credentials saved to the private credential store.")
	fmt.Fprintln(stdout, "Environment variables and flat config credentials still take precedence when present.")
}

func printLoginError(stderr io.Writer, action string, err error, redactor auth.Redactor) {
	switch {
	case errors.Is(err, context.Canceled):
		fmt.Fprintf(stderr, "%s: login canceled\n", action)
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintf(stderr, "%s: login timed out\n", action)
	default:
		fmt.Fprintf(stderr, "%s: %s\n", action, redactor.Redact(err.Error()))
	}
}

type localLoginCallbackWaiter struct {
	server *http.Server
	done   chan string
	errs   chan error

	closeOnce sync.Once
	closeErr  error
}

func newLocalLoginCallbackWaiter(rawRedirectURI string) (*localLoginCallbackWaiter, error) {
	parsed, err := validateLocalLoginRedirectURI(rawRedirectURI)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(parsed.Hostname(), parsed.Port()))
	if err != nil {
		return nil, fmt.Errorf("listen on OAuth callback address: %w", err)
	}

	waiter := &localLoginCallbackWaiter{
		done: make(chan string, 1),
		errs: make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(parsed.EscapedPath(), func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL == nil || r.URL.EscapedPath() != parsed.EscapedPath() {
			http.NotFound(w, r)
			return
		}

		rawCallbackURL := "http://" + r.Host + r.URL.RequestURI()
		select {
		case waiter.done <- rawCallbackURL:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "twi received the Twitch login callback. You can return to the terminal.")
		default:
			http.Error(w, "callback already received", http.StatusConflict)
		}
	})

	waiter.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := waiter.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case waiter.errs <- err:
			default:
			}
		}
	}()
	return waiter, nil
}

func (w *localLoginCallbackWaiter) Wait(ctx context.Context, expectedState auth.Secret) (auth.LoginCallback, error) {
	select {
	case rawCallbackURL := <-w.done:
		request, err := http.NewRequest(http.MethodGet, rawCallbackURL, nil)
		if err != nil {
			return auth.LoginCallback{}, fmt.Errorf("parse OAuth callback: %w", err)
		}
		return auth.LoginCallbackFromRequest(request, expectedState), nil
	case err := <-w.errs:
		return auth.LoginCallback{}, err
	case <-ctx.Done():
		return auth.LoginCallback{}, ctx.Err()
	}
}

func (w *localLoginCallbackWaiter) Close() error {
	w.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		w.closeErr = w.server.Shutdown(ctx)
	})
	return w.closeErr
}

func validateLocalLoginRedirectURI(rawRedirectURI string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawRedirectURI))
	if err != nil {
		return nil, fmt.Errorf("parse redirect URI: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil, errors.New("redirect URI must use http with localhost or 127.0.0.1")
	}
	if parsed.User != nil {
		return nil, errors.New("redirect URI must not include user info")
	}
	host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, errors.New("redirect URI host must be localhost, 127.0.0.1, or ::1")
	}
	if parsed.Port() == "" {
		return nil, errors.New("redirect URI must include an explicit localhost port")
	}
	if strings.TrimSpace(parsed.EscapedPath()) == "" || parsed.EscapedPath() == "/" {
		return nil, errors.New("redirect URI must include a callback path")
	}
	return parsed, nil
}

func openBrowser(ctx context.Context, targetURL string) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return errors.New("authorization URL is empty")
	}

	var candidates [][]string
	if browser := strings.TrimSpace(os.Getenv("BROWSER")); browser != "" {
		parts := strings.Fields(browser)
		if len(parts) > 0 {
			candidates = append(candidates, append(parts, targetURL))
		}
	}

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, []string{"open", targetURL})
	default:
		candidates = append(candidates, []string{"xdg-open", targetURL})
	}

	var attempted []string
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			attempted = append(attempted, candidate[0])
			continue
		}
		cmd := exec.CommandContext(ctx, path, candidate[1:]...)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() {
			_ = cmd.Wait()
		}()
		return nil
	}
	if len(attempted) == 0 {
		return errors.New("automatic browser opening is not supported in this environment")
	}
	return fmt.Errorf("automatic browser opening is not supported in this environment; tried %s", strings.Join(attempted, ", "))
}

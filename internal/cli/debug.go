package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/w0rxbend/twi/internal/app"
	"github.com/w0rxbend/twi/internal/assets"
	"github.com/w0rxbend/twi/internal/auth"
	"github.com/w0rxbend/twi/internal/config"
	"github.com/w0rxbend/twi/internal/debuglog"
)

type debugFlagOptions struct {
	enabled optionalBoolFlag
	path    string
}

func addDebugFlags(fs *flag.FlagSet, opts *debugFlagOptions) {
	fs.Var(&opts.enabled, "debug-log", "enable redacted structured debug logging")
	fs.StringVar(&opts.path, "debug-log-path", "", "debug log file path; defaults to the twi cache directory")
}

func applyDebugFlagOverrides(overrides *config.Overrides, opts debugFlagOptions) {
	if opts.enabled.set {
		overrides.DebugLogSet = true
		overrides.DebugLogEnabled = opts.enabled.value
	}
	if strings.TrimSpace(opts.path) != "" {
		overrides.DebugLogPath = opts.path
	}
}

func openDebugLogger(cfg config.Config) (debuglog.Logger, func(), error) {
	if !cfg.Debug.Enabled {
		return debuglog.Logger{}, func() {}, nil
	}
	path := strings.TrimSpace(cfg.Debug.LogPath)
	if path == "" {
		cacheDir, err := config.DefaultCacheDir()
		if err != nil {
			return debuglog.Logger{}, func() {}, err
		}
		path = filepath.Join(cacheDir, "debug.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return debuglog.Logger{}, func() {}, err
	}
	if err := validateDebugLogPath(path); err != nil {
		return debuglog.Logger{}, func() {}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return debuglog.Logger{}, func() {}, err
	}
	logger := debuglog.New(file, debuglog.Options{
		Enabled: true,
		Secrets: []auth.Secret{
			auth.NewSecret(cfg.Twitch.OAuthToken),
			auth.NewSecret(cfg.Twitch.RefreshToken),
			auth.NewSecret(cfg.Twitch.ClientSecret),
		},
	})
	logger.Log(context.Background(), "cli.debug_log.opened")
	return logger, func() { _ = file.Close() }, nil
}

func validateDebugLogPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("debug log path is a directory: %s", config.RedactDisplayValue(path))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("debug log path must not be a symlink: %s", config.RedactDisplayValue(path))
	}
	if perms := info.Mode().Perm(); perms&0o077 != 0 {
		return fmt.Errorf("debug log file permissions %04o allow group/other access; require private user-only permissions", perms)
	}
	return nil
}

func withDebugLogger(opts app.ClientOptions, logger debuglog.Logger) app.ClientOptions {
	opts.DebugLogger = logger
	if resolver, ok := opts.AssetResolver.(*assets.Resolver); ok {
		resolver.Logger = logger
		if downloader, ok := resolver.Downloader.(*assets.PublicImageDownloader); ok {
			downloader.Options.Logger = logger
		}
	}
	return opts
}

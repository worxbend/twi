package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/worxbend/twi/internal/app"
	"github.com/worxbend/twi/internal/assets"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/debuglog"
)

const (
	debugLogDirMode  fs.FileMode = 0o700
	debugLogFileMode fs.FileMode = 0o600
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
	if err := os.MkdirAll(filepath.Dir(path), debugLogDirMode); err != nil {
		return debuglog.Logger{}, func() {}, err
	}
	file, err := openDebugLogFile(path)
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

func validateOpenedDebugLogFile(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return debugLogOperationError("stat", path, err)
	}
	return validateDebugLogFileInfo(path, info)
}

func validateDebugLogPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return validateDebugLogFileInfo(path, info)
}

func validateDebugLogFileInfo(path string, info fs.FileInfo) error {
	if info.IsDir() {
		return fmt.Errorf("debug log path is a directory: %s", config.RedactDisplayValue(path))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("debug log path must not be a symlink: %s", config.RedactDisplayValue(path))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("debug log path is not a regular file: %s", config.RedactDisplayValue(path))
	}
	if perms := info.Mode().Perm(); perms&0o077 != 0 {
		return fmt.Errorf("debug log file permissions %04o allow group/other access; require private user-only permissions: %s", perms, config.RedactDisplayValue(path))
	}
	return nil
}

func debugLogOpenFileError(path string, err error) error {
	if err == nil {
		return nil
	}
	if debugLogOpenErrorIsSymlink(err) {
		return fmt.Errorf("debug log path must not be a symlink: %s", config.RedactDisplayValue(path))
	}
	if statErr := validateDebugLogPath(path); statErr == nil {
		return debugLogOperationError("open", path, err)
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return debugLogOperationError("open", path, err)
}

func debugLogOperationError(action, path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s debug log file %s: %s", action, config.RedactDisplayValue(path), safeDebugLogErrorDetail(err))
}

func safeDebugLogErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return config.RedactDisplayValue(pathErr.Err.Error())
	}
	return config.RedactDisplayValue(err.Error())
}

func closeDebugLogFileWithError(file *os.File, err error) (*os.File, error) {
	if file != nil {
		_ = file.Close()
	}
	return nil, err
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

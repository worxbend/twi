//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/config"
)

func TestDebugLogOpenUnixPlatformDocumentsNoFollowGuarantee(t *testing.T) {
	if !debugLogOpenUsesNoFollow {
		t.Fatal("Unix debug log opener should advertise no-follow opening")
	}
	if !strings.Contains(debugLogOpenPlatformNote, "O_NOFOLLOW") || !strings.Contains(debugLogOpenPlatformNote, "file descriptor") {
		t.Fatalf("Unix platform note does not document no-follow descriptor validation: %q", debugLogOpenPlatformNote)
	}
}

func TestOpenDebugLoggerUnixCreatesPrivateParentAndFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nested", "debug.log")
	cfg := config.Default()
	cfg.Debug.Enabled = true
	cfg.Debug.LogPath = logPath

	logger, closeLog, err := openDebugLogger(cfg)
	if err != nil {
		t.Fatalf("openDebugLogger returned error: %v", err)
	}
	if !logger.Enabled() {
		t.Fatal("debug logger disabled, want enabled")
	}
	closeLog()

	dirInfo, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatalf("stat debug log parent dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != debugLogDirMode {
		t.Fatalf("debug log parent dir mode = %04o, want %04o", got, debugLogDirMode)
	}
	fileInfo, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat debug log file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != debugLogFileMode {
		t.Fatalf("debug log file mode = %04o, want %04o", got, debugLogFileMode)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read debug log file: %v", err)
	}
	if !strings.Contains(string(data), `"event":"cli.debug_log.opened"`) {
		t.Fatalf("debug log missing opened event:\n%s", string(data))
	}
}

func TestOpenDebugLogFileUnixRejectsExistingGroupReadableFileWithRedactedPath(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "debug.log?client_secret=debug-path-secret")
	if err := os.WriteFile(logPath, []byte("existing\n"), debugLogFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logPath, 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := openDebugLogFile(logPath)
	if err == nil {
		_ = file.Close()
		t.Fatal("openDebugLogFile succeeded, want private-permission failure")
	}
	if !strings.Contains(err.Error(), "private user-only") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error missing redacted private-permission detail: %v", err)
	}
	if strings.Contains(err.Error(), "debug-path-secret") {
		t.Fatalf("error leaked credential-shaped path: %v", err)
	}
}

func TestOpenDebugLogFileUnixRejectsDirectoryWithRedactedPath(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "debug.log?code=debug-dir-secret")
	if err := os.Mkdir(logPath, debugLogDirMode); err != nil {
		t.Fatal(err)
	}

	file, err := openDebugLogFile(logPath)
	if err == nil {
		_ = file.Close()
		t.Fatal("openDebugLogFile succeeded, want directory failure")
	}
	if !strings.Contains(err.Error(), "debug log path is a directory") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error missing redacted directory detail: %v", err)
	}
	if strings.Contains(err.Error(), "debug-dir-secret") {
		t.Fatalf("error leaked credential-shaped path: %v", err)
	}
}

func TestOpenDebugLogFileUnixRejectsSymlinkWithRedactedPath(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target.log")
	if err := os.WriteFile(targetPath, []byte("target\n"), debugLogFileMode); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "debug.log?state=debug-link-secret")
	if err := os.Symlink(targetPath, logPath); err != nil {
		t.Fatal(err)
	}

	file, err := openDebugLogFile(logPath)
	if err == nil {
		_ = file.Close()
		t.Fatal("openDebugLogFile succeeded, want symlink failure")
	}
	if !strings.Contains(err.Error(), "debug log path must not be a symlink") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error missing redacted symlink detail: %v", err)
	}
	if strings.Contains(err.Error(), "debug-link-secret") {
		t.Fatalf("error leaked credential-shaped path: %v", err)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(data) != "target\n" {
		t.Fatalf("symlink target changed after rejected open: %q", string(data))
	}
}

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/w0rxbend/twi/internal/auth"
)

func TestCredentialRecordFromLoginResultAndRedaction(t *testing.T) {
	updatedAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	expiresAt := updatedAt.Add(time.Hour)
	record := CredentialRecordFromLoginResult(auth.LoginResult{
		Identity: auth.Identity{
			UserID:      "42",
			Login:       "viewer",
			DisplayName: "Viewer",
		},
		Tokens: auth.TokenSet{
			AccessToken:  auth.NewSecret("oauth:access-secret"),
			RefreshToken: auth.NewSecret("refresh-secret"),
			TokenType:    "bearer",
			Scopes:       auth.RequiredChatScopes(),
			ExpiresAt:    expiresAt,
		},
	}, "client-id", updatedAt)

	if record.UserID != "42" || record.Login != "viewer" || record.ClientID != "client-id" {
		t.Fatalf("record identity = %#v, want login result fields", record)
	}
	if record.AccessToken.Reveal() != "oauth:access-secret" || record.RefreshToken.Reveal() != "refresh-secret" {
		t.Fatalf("record token fields were not preserved")
	}
	if !reflect.DeepEqual(record.Scopes, auth.RequiredChatScopes()) {
		t.Fatalf("scopes = %#v, want required chat scopes", record.Scopes)
	}

	formatted := fmt.Sprintf("%+v %#v", record, record)
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal record returned error: %v", err)
	}
	for _, output := range []string{formatted, string(encoded)} {
		for _, raw := range []string{"oauth:access-secret", "access-secret", "refresh-secret"} {
			if strings.Contains(output, raw) {
				t.Fatalf("default credential output leaked %q: %s", raw, output)
			}
		}
		if !strings.Contains(output, "redacted") {
			t.Fatalf("default credential output missing redaction marker: %s", output)
		}
	}
}

func TestCredentialFileMarshalIsExplicitRevealPath(t *testing.T) {
	record := credentialFixture()

	data, err := MarshalCredentialFile(record)
	if err != nil {
		t.Fatalf("MarshalCredentialFile returned error: %v", err)
	}
	got := string(data)
	for _, want := range []struct {
		name string
		text string
	}{
		{name: "version", text: `"version": 1`},
		{name: "access token field", text: `"access_token": "oauth:access-secret"`},
		{name: "refresh token field", text: `"refresh_token": "refresh-secret"`},
		{name: "scope list", text: `"scopes": [`},
		{name: "chat read scope", text: `"chat:read"`},
		{name: "chat edit scope", text: `"chat:edit"`},
	} {
		if !strings.Contains(got, want.text) {
			t.Fatalf("credential file missing %s", want.name)
		}
	}
	if strings.Contains(got, "<redacted>") {
		t.Fatal("credential file used redacted token values")
	}

	parsed, err := ParseCredentialFile(data)
	if err != nil {
		t.Fatalf("ParseCredentialFile returned error: %v", err)
	}
	if parsed.AccessToken.Reveal() != record.AccessToken.Reveal() {
		t.Fatal("parsed access token did not round-trip")
	}
	if parsed.RefreshToken.Reveal() != record.RefreshToken.Reveal() {
		t.Fatal("parsed refresh token did not round-trip")
	}
	if !reflect.DeepEqual(parsed.Scopes, record.Scopes) {
		t.Fatalf("parsed scopes = %#v, want %#v", parsed.Scopes, record.Scopes)
	}
}

func TestParseCredentialFileRejectsUnsupportedFormatAndUnknownFields(t *testing.T) {
	if _, err := ParseCredentialFile([]byte(`{"version":2,"twitch":{}}`)); !errors.Is(err, ErrUnsupportedCredentialFileFormat) {
		t.Fatalf("ParseCredentialFile version error = %v, want ErrUnsupportedCredentialFileFormat", err)
	}
	if _, err := ParseCredentialFile([]byte(`{"version":1,"twitch":{},"access_token":"oauth:secret"}`)); err == nil {
		t.Fatal("ParseCredentialFile accepted unknown top-level field")
	} else if strings.Contains(err.Error(), "oauth:secret") {
		t.Fatalf("ParseCredentialFile error leaked unknown field value: %v", err)
	}

	if _, err := ParseCredentialFile([]byte(`{"version":1,"twitch":{"expires_at":"oauth:timestamp-secret"}}`)); err == nil {
		t.Fatal("ParseCredentialFile accepted malformed timestamp")
	} else if strings.Contains(err.Error(), "oauth:timestamp-secret") || strings.Contains(err.Error(), "timestamp-secret") {
		t.Fatal("ParseCredentialFile timestamp error leaked raw value")
	}
}

func TestCredentialFilePlanUsesRestrictiveDefaults(t *testing.T) {
	plan, err := NewCredentialFilePlan(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatalf("NewCredentialFilePlan returned error: %v", err)
	}
	if plan.DirectoryMode != CredentialDirectoryMode {
		t.Fatalf("directory mode = %s, want %s", plan.DirectoryMode, CredentialDirectoryMode)
	}
	if plan.DirectoryMode != fs.FileMode(0o700) {
		t.Fatalf("directory mode = %s, want 0700", plan.DirectoryMode)
	}
	if plan.FileMode != CredentialFileMode {
		t.Fatalf("file mode = %s, want %s", plan.FileMode, CredentialFileMode)
	}
	if plan.FileMode != fs.FileMode(0o600) {
		t.Fatalf("file mode = %s, want 0600", plan.FileMode)
	}
	if plan.FormatVersion != CredentialFileRecordVersion {
		t.Fatalf("format version = %d, want %d", plan.FormatVersion, CredentialFileRecordVersion)
	}
	if plan.Migration != CredentialMigrationExplicitOnly {
		t.Fatalf("migration = %q, want explicit-only", plan.Migration)
	}
}

func TestCredentialPermissionValidationRejectsGroupWorldAndExecutableModes(t *testing.T) {
	if err := ValidateCredentialFileMode(0o600); err != nil {
		t.Fatalf("ValidateCredentialFileMode(0600) returned error: %v", err)
	}
	for _, mode := range []fs.FileMode{0o700, 0o640, 0o604, 0o666, 0o777, 0o400, 0o200, 0o000, fs.ModeSetuid | 0o600, fs.ModeSetgid | 0o600, fs.ModeSticky | 0o600} {
		if err := ValidateCredentialFileMode(fs.FileMode(mode)); !errors.Is(err, ErrInsecureCredentialPermissions) {
			t.Fatalf("ValidateCredentialFileMode(%#o) = %v, want ErrInsecureCredentialPermissions", mode, err)
		}
	}

	if err := ValidateCredentialDirectoryMode(0o700); err != nil {
		t.Fatalf("ValidateCredentialDirectoryMode(0700) returned error: %v", err)
	}
	for _, mode := range []fs.FileMode{0o750, 0o705, 0o777, 0o500, 0o300, 0o000, fs.ModeSetuid | 0o700, fs.ModeSetgid | 0o700, fs.ModeSticky | 0o700} {
		if err := ValidateCredentialDirectoryMode(fs.FileMode(mode)); !errors.Is(err, ErrInsecureCredentialPermissions) {
			t.Fatalf("ValidateCredentialDirectoryMode(%#o) = %v, want ErrInsecureCredentialPermissions", mode, err)
		}
	}
}

func TestCredentialFilePlanValidationRejectsUnsafeModesAndMigration(t *testing.T) {
	plan := CredentialFilePlan{
		Path:          filepath.Join(t.TempDir(), "credentials.json"),
		DirectoryMode: 0o700,
		FileMode:      0o640,
		FormatVersion: CredentialFileRecordVersion,
		Migration:     CredentialMigrationExplicitOnly,
	}
	if err := plan.Validate(); !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("Validate insecure file mode = %v, want ErrInsecureCredentialPermissions", err)
	}

	plan.FileMode = 0o600
	plan.Migration = CredentialMigration("automatic-copy")
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted unsupported migration policy")
	}
}

func TestCredentialFileStoreRejectsUnsupportedPlatformPolicy(t *testing.T) {
	plan, err := NewCredentialFilePlan(filepath.Join(t.TempDir(), "credentials.json?state=state-secret&code=callback-secret"))
	if err != nil {
		t.Fatalf("NewCredentialFilePlan returned error: %v", err)
	}

	_, err = newCredentialFileStoreForPlatform(plan, credentialFilePlatform{
		Supported: false,
		Reason:    "credential-file fallback is disabled on this platform because ACL no-follow support is unavailable",
		Action:    "use environment variables or a private flat config file",
	})
	if !errors.Is(err, ErrUnsupportedCredentialFilePlatform) {
		t.Fatalf("NewCredentialFileStore unsupported platform error = %v, want ErrUnsupportedCredentialFilePlatform", err)
	}
	for _, want := range []string{"credential-file fallback is disabled", "environment variables", "private flat config file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unsupported platform error missing %q: %v", want, err)
		}
	}
	assertCredentialErrorDoesNotLeak(t, err, "state-secret", "callback-secret")
}

func TestCredentialFileStoreSaveLoadAndDeleteUseRestrictiveModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twi", "credentials.json")
	store := newCredentialFileStoreForTest(t, path)
	record := credentialFixture()

	if err := store.SaveCredentials(context.Background(), record); err != nil {
		t.Fatalf("SaveCredentials returned error: %v", err)
	}

	assertMode(t, filepath.Dir(path), CredentialDirectoryMode)
	assertMode(t, path, CredentialFileMode)
	if mode := fileMode(t, path).Perm(); mode&0o077 != 0 {
		t.Fatalf("credential file mode = %s, want no group/world bits", mode)
	}

	loaded, ok, err := store.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials returned error: %v", err)
	}
	if !ok {
		t.Fatal("LoadCredentials ok = false, want true")
	}
	if loaded.AccessToken.Reveal() != record.AccessToken.Reveal() {
		t.Fatal("loaded access token did not round-trip")
	}
	if loaded.RefreshToken.Reveal() != record.RefreshToken.Reveal() {
		t.Fatal("loaded refresh token did not round-trip")
	}

	if err := store.DeleteCredentials(context.Background()); err != nil {
		t.Fatalf("DeleteCredentials returned error: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential file after delete stat error = %v, want not exist", err)
	}
}

func TestCredentialFileStoreOverwriteReplacesAtomicallyWithoutTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twi")
	path := filepath.Join(dir, "credentials.json")
	store := newCredentialFileStoreForTest(t, path)

	oldRecord := credentialFixture()
	oldRecord.AccessToken = auth.NewSecret("oauth:old-access-secret")
	oldRecord.RefreshToken = auth.NewSecret("old-refresh-secret")
	if err := store.SaveCredentials(context.Background(), oldRecord); err != nil {
		t.Fatalf("initial SaveCredentials returned error: %v", err)
	}
	oldBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial credential file: %v", err)
	}
	if !strings.Contains(string(oldBytes), "oauth:old-access-secret") {
		t.Fatal("initial credential file missing old token fixture")
	}

	newRecord := credentialFixture()
	newRecord.AccessToken = auth.NewSecret("oauth:new-access-secret")
	newRecord.RefreshToken = auth.NewSecret("new-refresh-secret")
	newRecord.UpdatedAt = newRecord.UpdatedAt.Add(time.Minute)
	if err := store.SaveCredentials(context.Background(), newRecord); err != nil {
		t.Fatalf("overwrite SaveCredentials returned error: %v", err)
	}

	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overwritten credential file: %v", err)
	}
	got := string(gotBytes)
	if strings.Contains(got, "oauth:old-access-secret") || strings.Contains(got, "old-refresh-secret") {
		t.Fatalf("overwritten credential file retained old token: %s", got)
	}
	if !strings.Contains(got, "oauth:new-access-secret") || !strings.Contains(got, "new-refresh-secret") {
		t.Fatalf("overwritten credential file missing new tokens: %s", got)
	}
	assertNoCredentialTemps(t, dir)
	assertMode(t, path, CredentialFileMode)
}

func TestCredentialFileStoreFailedSaveKeepsExistingFile(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("directory mode write-denial check is Unix-specific")
	}

	dir := filepath.Join(t.TempDir(), "twi")
	path := filepath.Join(dir, "credentials.json")
	store := newCredentialFileStoreForTest(t, path)
	oldRecord := credentialFixture()
	if err := store.SaveCredentials(context.Background(), oldRecord); err != nil {
		t.Fatalf("initial SaveCredentials returned error: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial credential file: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod credential dir read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, CredentialDirectoryMode)
	})

	newRecord := credentialFixture()
	newRecord.AccessToken = auth.NewSecret("oauth:new-access-secret")
	err = store.SaveCredentials(context.Background(), newRecord)
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("SaveCredentials with unsafe dir mode error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:new-access-secret", "new-access-secret", "refresh-secret")

	if chmodErr := os.Chmod(dir, CredentialDirectoryMode); chmodErr != nil {
		t.Fatalf("restore credential dir mode: %v", chmodErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file after failed save: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("credential file changed after failed save\nbefore=%s\nafter=%s", string(before), string(after))
	}
}

func TestCredentialFileStoreLoadMissingFile(t *testing.T) {
	store := newCredentialFileStoreForTest(t, filepath.Join(t.TempDir(), "missing", "credentials.json"))

	record, ok, err := store.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials returned error: %v", err)
	}
	if ok {
		t.Fatalf("LoadCredentials ok = true with record %#v, want missing", record)
	}

	dir := filepath.Join(t.TempDir(), "twi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir unsafe empty credential dir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod unsafe empty credential dir: %v", err)
	}
	store = newCredentialFileStoreForTest(t, filepath.Join(dir, "credentials.json"))
	if _, ok, err := store.LoadCredentials(context.Background()); err != nil || ok {
		t.Fatalf("LoadCredentials with missing file in unsafe dir = ok %v err %v, want missing without error", ok, err)
	}
}

func TestCredentialFileStoreRejectsMalformedFilesWithRedactedErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twi")
	path := filepath.Join(dir, "credentials.json")
	if err := os.MkdirAll(dir, CredentialDirectoryMode); err != nil {
		t.Fatalf("mkdir credential dir: %v", err)
	}
	if err := os.Chmod(dir, CredentialDirectoryMode); err != nil {
		t.Fatalf("chmod credential dir: %v", err)
	}
	malformed := `{"version":1,"twitch":{"access_token":"oauth:access-secret","refresh_token":"refresh-secret","expires_at":"oauth:timestamp-secret"}}`
	if err := os.WriteFile(path, []byte(malformed), CredentialFileMode); err != nil {
		t.Fatalf("write malformed credential file: %v", err)
	}
	if err := os.Chmod(path, CredentialFileMode); err != nil {
		t.Fatalf("chmod credential file: %v", err)
	}

	store := newCredentialFileStoreForTest(t, path)
	_, _, err := store.LoadCredentials(context.Background())
	if !errors.Is(err, ErrMalformedCredentialFile) {
		t.Fatalf("LoadCredentials malformed error = %v, want ErrMalformedCredentialFile", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:access-secret", "access-secret", "refresh-secret", "oauth:timestamp-secret", "timestamp-secret")

	if err := os.WriteFile(path, []byte(`{"version":2,"twitch":{"access_token":"oauth:access-secret"}}`), CredentialFileMode); err != nil {
		t.Fatalf("write unsupported credential file: %v", err)
	}
	_, _, err = store.LoadCredentials(context.Background())
	if !errors.Is(err, ErrUnsupportedCredentialFileFormat) || !errors.Is(err, ErrMalformedCredentialFile) {
		t.Fatalf("LoadCredentials unsupported error = %v, want unsupported malformed file", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:access-secret", "access-secret")

	valid, err := MarshalCredentialFile(credentialFixture())
	if err != nil {
		t.Fatalf("marshal credential fixture: %v", err)
	}
	trailing := append(valid, []byte(`{"access_token":"oauth:trailing-secret"}`)...)
	if err := os.WriteFile(path, trailing, CredentialFileMode); err != nil {
		t.Fatalf("write trailing credential file: %v", err)
	}
	_, _, err = store.LoadCredentials(context.Background())
	if !errors.Is(err, ErrMalformedCredentialFile) {
		t.Fatalf("LoadCredentials trailing data error = %v, want ErrMalformedCredentialFile", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:trailing-secret", "trailing-secret")
}

func TestCredentialFileStoreRejectsUnsafePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twi")
	path := filepath.Join(dir, "credentials.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir credential dir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod credential dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), CredentialFileMode); err != nil {
		t.Fatalf("write credential file: %v", err)
	}

	store := newCredentialFileStoreForTest(t, path)
	if _, _, err := store.LoadCredentials(context.Background()); !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("LoadCredentials unsafe dir error = %v, want ErrInsecureCredentialPermissions", err)
	}
	if err := store.SaveCredentials(context.Background(), credentialFixture()); !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("SaveCredentials unsafe dir error = %v, want ErrInsecureCredentialPermissions", err)
	}

	if err := os.Chmod(dir, CredentialDirectoryMode); err != nil {
		t.Fatalf("restore credential dir mode: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod credential file unsafe: %v", err)
	}
	if _, _, err := store.LoadCredentials(context.Background()); !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("LoadCredentials unsafe file error = %v, want ErrInsecureCredentialPermissions", err)
	}
	if err := store.SaveCredentials(context.Background(), credentialFixture()); !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("SaveCredentials unsafe file error = %v, want ErrInsecureCredentialPermissions", err)
	}
}

func TestCredentialFileStoreRejectsSymlinkWithoutFollowing(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("symlink permission checks are Unix-specific")
	}

	dir := filepath.Join(t.TempDir(), "twi")
	if err := os.MkdirAll(dir, CredentialDirectoryMode); err != nil {
		t.Fatalf("mkdir credential dir: %v", err)
	}
	if err := os.Chmod(dir, CredentialDirectoryMode); err != nil {
		t.Fatalf("chmod credential dir: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	targetRecord := credentialFixture()
	targetRecord.AccessToken = auth.NewSecret("oauth:target-secret")
	targetData, err := MarshalCredentialFile(targetRecord)
	if err != nil {
		t.Fatalf("marshal target credential file: %v", err)
	}
	if err := os.WriteFile(target, targetData, CredentialFileMode); err != nil {
		t.Fatalf("write target credential file: %v", err)
	}
	if err := os.Chmod(target, CredentialFileMode); err != nil {
		t.Fatalf("chmod target credential file: %v", err)
	}

	path := filepath.Join(dir, "credentials.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink credential path: %v", err)
	}
	store := newCredentialFileStoreForTest(t, path)

	_, _, err = store.LoadCredentials(context.Background())
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("LoadCredentials symlink error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:target-secret", "target-secret")

	saveRecord := credentialFixture()
	saveRecord.AccessToken = auth.NewSecret("oauth:new-access-secret")
	err = store.SaveCredentials(context.Background(), saveRecord)
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("SaveCredentials symlink error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:new-access-secret", "new-access-secret", "refresh-secret")

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat credential symlink after save: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("credential path mode = %s, want symlink still in place", info.Mode())
	}
	targetAfter, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after symlink save attempt: %v", err)
	}
	if string(targetAfter) != string(targetData) {
		t.Fatalf("symlink target changed after save attempt\nbefore=%s\nafter=%s", string(targetData), string(targetAfter))
	}
}

func TestCredentialFileStoreRejectsSymlinkDirectoryWithoutFollowing(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("symlink permission checks are Unix-specific")
	}

	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realDir, CredentialDirectoryMode); err != nil {
		t.Fatalf("mkdir real credential dir: %v", err)
	}
	if err := os.Chmod(realDir, CredentialDirectoryMode); err != nil {
		t.Fatalf("chmod real credential dir: %v", err)
	}
	realPath := filepath.Join(realDir, "credentials.json")
	targetRecord := credentialFixture()
	targetRecord.AccessToken = auth.NewSecret("oauth:target-secret")
	targetData, err := MarshalCredentialFile(targetRecord)
	if err != nil {
		t.Fatalf("marshal target credential file: %v", err)
	}
	if err := os.WriteFile(realPath, targetData, CredentialFileMode); err != nil {
		t.Fatalf("write real credential file: %v", err)
	}
	if err := os.Chmod(realPath, CredentialFileMode); err != nil {
		t.Fatalf("chmod real credential file: %v", err)
	}

	linkDir := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink credential dir: %v", err)
	}
	store := newCredentialFileStoreForTest(t, filepath.Join(linkDir, "credentials.json"))

	_, _, err = store.LoadCredentials(context.Background())
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("LoadCredentials symlink dir error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:target-secret", "target-secret")

	saveRecord := credentialFixture()
	saveRecord.AccessToken = auth.NewSecret("oauth:new-access-secret")
	err = store.SaveCredentials(context.Background(), saveRecord)
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("SaveCredentials symlink dir error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:new-access-secret", "new-access-secret", "refresh-secret")

	err = store.DeleteCredentials(context.Background())
	if !errors.Is(err, ErrInsecureCredentialPermissions) {
		t.Fatalf("DeleteCredentials symlink dir error = %v, want ErrInsecureCredentialPermissions", err)
	}
	assertCredentialErrorDoesNotLeak(t, err, "oauth:target-secret", "target-secret")

	targetAfter, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("read target after symlink dir attempts: %v", err)
	}
	if string(targetAfter) != string(targetData) {
		t.Fatalf("symlink dir target changed after attempts\nbefore=%s\nafter=%s", string(targetData), string(targetAfter))
	}
}

func TestMemoryCredentialStoreClonesRecordsAndTracksOperations(t *testing.T) {
	store := NewMemoryCredentialStore()
	record := credentialFixture()
	if err := store.SaveCredentials(context.Background(), record); err != nil {
		t.Fatalf("SaveCredentials returned error: %v", err)
	}
	record.Scopes[0] = auth.ScopeChatEdit

	got, ok, err := store.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials returned error: %v", err)
	}
	if !ok {
		t.Fatal("LoadCredentials ok = false, want true")
	}
	if !reflect.DeepEqual(got.Scopes, []auth.Scope{auth.ScopeChatRead, auth.ScopeChatEdit}) {
		t.Fatalf("loaded scopes = %#v, want saved snapshot", got.Scopes)
	}
	got.Scopes[0] = auth.ScopeChatEdit
	again, _, err := store.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials second call returned error: %v", err)
	}
	if again.Scopes[0] != auth.ScopeChatRead {
		t.Fatalf("LoadCredentials returned mutable backing slice: %#v", again.Scopes)
	}

	saves := store.SavedRecords()
	saves[0].Scopes[0] = auth.ScopeChatEdit
	if store.SavedRecords()[0].Scopes[0] != auth.ScopeChatRead {
		t.Fatalf("SavedRecords returned mutable backing slice")
	}

	if err := store.DeleteCredentials(context.Background()); err != nil {
		t.Fatalf("DeleteCredentials returned error: %v", err)
	}
	if store.DeleteCount() != 1 {
		t.Fatalf("DeleteCount = %d, want 1", store.DeleteCount())
	}
	if _, ok, err := store.LoadCredentials(context.Background()); err != nil || ok {
		t.Fatalf("LoadCredentials after delete = ok %v err %v, want miss", ok, err)
	}
}

func TestCredentialStoreFakesHonorCancellationAndConfiguredErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := NewMemoryCredentialStore().LoadCredentials(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("MemoryCredentialStore LoadCredentials canceled error = %v, want context.Canceled", err)
	}
	if err := (FailingCredentialStore{}).SaveCredentials(ctx, CredentialRecord{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("FailingCredentialStore SaveCredentials canceled error = %v, want context.Canceled", err)
	}

	wantErr := errors.New("fixture store failure")
	store := NewMemoryCredentialStore()
	store.SetErrors(wantErr, wantErr, wantErr)
	if _, _, err := store.LoadCredentials(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("LoadCredentials configured error = %v, want %v", err, wantErr)
	}
	if err := store.SaveCredentials(context.Background(), CredentialRecord{}); !errors.Is(err, wantErr) {
		t.Fatalf("SaveCredentials configured error = %v, want %v", err, wantErr)
	}
	if err := store.DeleteCredentials(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("DeleteCredentials configured error = %v, want %v", err, wantErr)
	}

	failing := FailingCredentialStore{Err: wantErr}
	if _, _, err := failing.LoadCredentials(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("FailingCredentialStore LoadCredentials error = %v, want %v", err, wantErr)
	}
}

func credentialFixture() CredentialRecord {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	return CredentialRecord{
		UserID:       "42",
		Login:        "viewer",
		DisplayName:  "Viewer",
		ClientID:     "client-id",
		AccessToken:  auth.NewSecret("oauth:access-secret"),
		RefreshToken: auth.NewSecret("refresh-secret"),
		TokenType:    "bearer",
		Scopes:       []auth.Scope{auth.ScopeChatRead, auth.ScopeChatEdit},
		ExpiresAt:    now.Add(time.Hour),
		UpdatedAt:    now,
	}
}

func newCredentialFileStoreForTest(t *testing.T, path string) *CredentialFileStore {
	t.Helper()
	plan, err := NewCredentialFilePlan(path)
	if err != nil {
		t.Fatalf("NewCredentialFilePlan returned error: %v", err)
	}
	store, err := NewCredentialFileStore(plan)
	if errors.Is(err, ErrUnsupportedCredentialFilePlatform) {
		t.Skipf("credential-file fallback unsupported on this platform: %v", err)
	}
	if err != nil {
		t.Fatalf("NewCredentialFileStore returned error: %v", err)
	}
	return store
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	if got := fileMode(t, path).Perm(); got != want {
		t.Fatalf("%s mode = %s, want %s", path, got, want)
	}
}

func fileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.Mode()
}

func assertNoCredentialTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read credential dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("credential temp file remained after save: %s", entry.Name())
		}
	}
}

func assertCredentialErrorDoesNotLeak(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error is nil")
	}
	text := fmt.Sprintf("%v", err)
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("credential error leaked %q: %s", secret, text)
		}
	}
}

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/w0rxbend/twi/internal/auth"
)

const (
	// CredentialFileRecordVersion is the JSON record version used by the
	// restrictive file fallback credential store.
	CredentialFileRecordVersion = 1
	// CredentialDirectoryMode is the mode used for directories that contain
	// persisted credential material.
	CredentialDirectoryMode fs.FileMode = 0o700
	// CredentialFileMode is the only mode used when creating fallback
	// credential files.
	CredentialFileMode fs.FileMode = 0o600
)

var (
	// ErrInsecureCredentialPermissions reports a file or directory mode that is
	// unsuitable for credential storage.
	ErrInsecureCredentialPermissions = errors.New("insecure credential permissions")
	// ErrUnsupportedCredentialFilePlatform reports that the restrictive local
	// credential-file fallback is unavailable for the current platform.
	ErrUnsupportedCredentialFilePlatform = errors.New("unsupported credential file platform")
	// ErrUnsupportedCredentialFileFormat reports a credential record with an
	// unsupported schema version.
	ErrUnsupportedCredentialFileFormat = errors.New("unsupported credential file format")
	// ErrMalformedCredentialFile reports a credential file that could not be
	// decoded as the storage-owned fallback JSON format.
	ErrMalformedCredentialFile = errors.New("malformed credential file")
)

// CredentialStore is the internal boundary for persisted Twitch OAuth
// credentials. Implementations must keep raw token values out of formatted
// errors, logs, diagnostics, and default structured output.
type CredentialStore interface {
	LoadCredentials(context.Context) (CredentialRecord, bool, error)
	SaveCredentials(context.Context, CredentialRecord) error
	DeleteCredentials(context.Context) error
}

// CredentialRecord is the storage-owned auth DTO. Tokens remain auth.Secret
// values so default fmt and JSON encoding redact them; fallback file storage
// must use MarshalCredentialFile to deliberately reveal token values.
type CredentialRecord struct {
	UserID       string
	Login        string
	DisplayName  string
	ClientID     string
	AccessToken  auth.Secret
	RefreshToken auth.Secret
	TokenType    string
	Scopes       []auth.Scope
	ExpiresAt    time.Time
	UpdatedAt    time.Time
}

// CredentialRecordFromLoginResult converts a completed login into the storage
// DTO without deciding where it will be persisted.
func CredentialRecordFromLoginResult(result auth.LoginResult, clientID string, updatedAt time.Time) CredentialRecord {
	scopes := result.Scopes
	if len(scopes) == 0 {
		scopes = result.Tokens.Scopes
	}
	return CredentialRecord{
		UserID:       result.Identity.UserID,
		Login:        result.Identity.Login,
		DisplayName:  result.Identity.DisplayName,
		ClientID:     clientID,
		AccessToken:  result.Tokens.AccessToken,
		RefreshToken: result.Tokens.RefreshToken,
		TokenType:    result.Tokens.TokenType,
		Scopes:       cloneCredentialScopes(scopes),
		ExpiresAt:    result.Tokens.ExpiresAt,
		UpdatedAt:    updatedAt,
	}
}

// Redactor returns an auth redactor configured with all secret values in the
// record.
func (r CredentialRecord) Redactor() auth.Redactor {
	return auth.NewRedactor(r.AccessToken, r.RefreshToken)
}

// Clone returns a deep copy of the record's mutable fields.
func (r CredentialRecord) Clone() CredentialRecord {
	r.Scopes = cloneCredentialScopes(r.Scopes)
	return r
}

// CredentialFilePlan describes the restrictive local JSON file fallback. This
// plan is intentionally separate from the current flat config file and from any
// future OS keychain backend.
type CredentialFilePlan struct {
	Path          string
	DirectoryMode fs.FileMode
	FileMode      fs.FileMode
	FormatVersion int
	Migration     CredentialMigration
}

// CredentialMigration documents how existing flat config/env credentials move
// into storage. T009 defines explicit migration only; T010 owns any file I/O.
type CredentialMigration string

const (
	// CredentialMigrationExplicitOnly means login/setup may save credentials
	// after user action, but config/env secrets are not copied automatically.
	CredentialMigrationExplicitOnly CredentialMigration = "explicit-only"
	maxCredentialFileBytes                              = 1 << 20
)

// DefaultCredentialFilePath returns the platform config-directory path for the
// fallback credential file.
func DefaultCredentialFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "twi", "credentials.json"), nil
}

// NewCredentialFilePlan returns the default fallback file plan for path. When
// path is empty, the platform config-directory credential path is used.
func NewCredentialFilePlan(path string) (CredentialFilePlan, error) {
	if path == "" {
		defaultPath, err := DefaultCredentialFilePath()
		if err != nil {
			return CredentialFilePlan{}, err
		}
		path = defaultPath
	}
	plan := CredentialFilePlan{
		Path:          path,
		DirectoryMode: CredentialDirectoryMode,
		FileMode:      CredentialFileMode,
		FormatVersion: CredentialFileRecordVersion,
		Migration:     CredentialMigrationExplicitOnly,
	}
	return plan, plan.Validate()
}

// Validate reports whether the plan uses a path, version, and restrictive
// modes suitable for storing credential material.
func (p CredentialFilePlan) Validate() error {
	if filepath.Clean(p.Path) == "." || p.Path == "" {
		return errors.New("credential file path is required")
	}
	if err := ValidateCredentialDirectoryMode(p.DirectoryMode); err != nil {
		return fmt.Errorf("credential directory mode: %w", err)
	}
	if err := ValidateCredentialFileMode(p.FileMode); err != nil {
		return fmt.Errorf("credential file mode: %w", err)
	}
	if p.FormatVersion != CredentialFileRecordVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedCredentialFileFormat, p.FormatVersion)
	}
	if p.Migration != CredentialMigrationExplicitOnly {
		return fmt.Errorf("unsupported credential migration policy: %s", p.Migration)
	}
	return nil
}

// ValidateCredentialDirectoryMode rejects credential directories that do not
// match the exact restrictive fallback directory mode.
func ValidateCredentialDirectoryMode(mode fs.FileMode) error {
	if mode.Perm() != CredentialDirectoryMode {
		return fmt.Errorf("%w: directory mode %s", ErrInsecureCredentialPermissions, mode.Perm())
	}
	if special := credentialSpecialModeBits(mode); special != 0 {
		return fmt.Errorf("%w: directory special mode bits %s", ErrInsecureCredentialPermissions, special)
	}
	return nil
}

// ValidateCredentialFileMode rejects credential files that do not match the
// exact restrictive fallback file mode.
func ValidateCredentialFileMode(mode fs.FileMode) error {
	if mode.Perm() != CredentialFileMode {
		return fmt.Errorf("%w: file mode %s", ErrInsecureCredentialPermissions, mode.Perm())
	}
	if special := credentialSpecialModeBits(mode); special != 0 {
		return fmt.Errorf("%w: file special mode bits %s", ErrInsecureCredentialPermissions, special)
	}
	return nil
}

func credentialSpecialModeBits(mode fs.FileMode) fs.FileMode {
	return mode & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}

// MarshalCredentialFile encodes the raw fallback credential file. This is the
// storage-owned reveal path for auth.Secret values; callers must not use it for
// diagnostics, logs, or user-facing output.
func MarshalCredentialFile(record CredentialRecord) ([]byte, error) {
	data, err := json.MarshalIndent(credentialFileFromRecord(record), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ParseCredentialFile decodes the fallback credential file format without
// exposing token values in errors.
func ParseCredentialFile(data []byte) (CredentialRecord, error) {
	var file credentialFileRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return CredentialRecord{}, fmt.Errorf("decode credential file: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return CredentialRecord{}, fmt.Errorf("%w: trailing data", ErrMalformedCredentialFile)
	}
	if file.Version != CredentialFileRecordVersion {
		return CredentialRecord{}, fmt.Errorf("%w: %d", ErrUnsupportedCredentialFileFormat, file.Version)
	}
	record, err := file.toRecord()
	if err != nil {
		return CredentialRecord{}, err
	}
	return record, nil
}

// CredentialFileStore persists Twitch OAuth credentials to the restrictive
// local JSON fallback file.
type CredentialFileStore struct {
	plan CredentialFilePlan
}

var _ CredentialStore = (*CredentialFileStore)(nil)

// NewDefaultCredentialFileStore creates the default platform config-directory
// fallback store.
func NewDefaultCredentialFileStore() (*CredentialFileStore, error) {
	plan, err := NewCredentialFilePlan("")
	if err != nil {
		return nil, err
	}
	return NewCredentialFileStore(plan)
}

// NewCredentialFileStore creates a fallback file store for plan.
func NewCredentialFileStore(plan CredentialFilePlan) (*CredentialFileStore, error) {
	return newCredentialFileStoreForPlatform(plan, credentialFilePlatformPolicy())
}

func newCredentialFileStoreForPlatform(plan CredentialFilePlan, platform credentialFilePlatform) (*CredentialFileStore, error) {
	if err := plan.Validate(); err != nil {
		return nil, credentialFileOperationError("validate credential file plan", plan.Path, err, auth.Redactor{})
	}
	if err := platform.validate(); err != nil {
		return nil, credentialFileOperationError("prepare credential storage", plan.Path, err, auth.Redactor{})
	}
	return &CredentialFileStore{plan: plan}, nil
}

// Plan returns the immutable fallback store plan.
func (s *CredentialFileStore) Plan() CredentialFilePlan {
	if s == nil {
		return CredentialFilePlan{}
	}
	return s.plan
}

// Path returns the fallback credential file path.
func (s *CredentialFileStore) Path() string {
	if s == nil {
		return ""
	}
	return s.plan.Path
}

// LoadCredentials reads and parses the fallback credential file. A missing
// directory or file is reported as an empty store.
func (s *CredentialFileStore) LoadCredentials(ctx context.Context) (CredentialRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CredentialRecord{}, false, err
	}
	if s == nil {
		return CredentialRecord{}, false, nil
	}
	if err := s.plan.Validate(); err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("validate credential file plan", s.plan.Path, err, auth.Redactor{})
	}

	info, err := os.Lstat(s.plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		return CredentialRecord{}, false, nil
	}
	if err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("load credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := validateCredentialDirectory(filepath.Dir(s.plan.Path)); err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("load credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := validateCredentialFileInfo(info); err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("load credential file", s.plan.Path, err, auth.Redactor{})
	}

	file, err := openCredentialFileNoFollow(s.plan.Path)
	if err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("load credential file", s.plan.Path, err, auth.Redactor{})
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCredentialFileBytes+1))
	if err != nil {
		return CredentialRecord{}, false, credentialFileOperationError("load credential file", s.plan.Path, err, auth.Redactor{})
	}
	if len(data) > maxCredentialFileBytes {
		return CredentialRecord{}, false, credentialFileMalformedError("load credential file", s.plan.Path, errors.New("credential file is too large"))
	}

	record, err := ParseCredentialFile(data)
	if err != nil {
		return CredentialRecord{}, false, credentialFileMalformedError("load credential file", s.plan.Path, err)
	}
	return record, true, nil
}

// SaveCredentials atomically replaces the fallback credential file with record.
func (s *CredentialFileStore) SaveCredentials(ctx context.Context, record CredentialRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	if err := s.plan.Validate(); err != nil {
		return credentialFileOperationError("validate credential file plan", s.plan.Path, err, record.Redactor())
	}

	data, err := MarshalCredentialFile(record)
	if err != nil {
		return credentialFileOperationError("marshal credential file", s.plan.Path, err, record.Redactor())
	}

	dir := filepath.Dir(s.plan.Path)
	if err := ensureCredentialDirectory(dir); err != nil {
		return credentialFileOperationError("prepare credential directory", dir, err, record.Redactor())
	}
	if err := validateCredentialReplacementTarget(s.plan.Path); err != nil {
		return credentialFileOperationError("prepare credential file", s.plan.Path, err, record.Redactor())
	}
	if err := writeCredentialFileAtomically(ctx, s.plan.Path, data, s.plan.FileMode); err != nil {
		return credentialFileOperationError("save credential file", s.plan.Path, err, record.Redactor())
	}
	return nil
}

// DeleteCredentials removes the fallback credential file without following a
// symlink at the credential-file path.
func (s *CredentialFileStore) DeleteCredentials(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	if err := s.plan.Validate(); err != nil {
		return credentialFileOperationError("validate credential file plan", s.plan.Path, err, auth.Redactor{})
	}
	info, err := os.Lstat(s.plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return credentialFileOperationError("delete credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := validateCredentialDirectory(filepath.Dir(s.plan.Path)); err != nil {
		return credentialFileOperationError("delete credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := validateCredentialFileInfo(info); err != nil {
		return credentialFileOperationError("delete credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := os.Remove(s.plan.Path); err != nil {
		return credentialFileOperationError("delete credential file", s.plan.Path, err, auth.Redactor{})
	}
	if err := syncCredentialDirectory(filepath.Dir(s.plan.Path)); err != nil {
		return credentialFileOperationError("sync credential directory", filepath.Dir(s.plan.Path), err, auth.Redactor{})
	}
	return nil
}

// MemoryCredentialStore is a stateful fake CredentialStore for tests.
type MemoryCredentialStore struct {
	mu        sync.RWMutex
	record    CredentialRecord
	present   bool
	loadErr   error
	saveErr   error
	deleteErr error
	saves     []CredentialRecord
	deletes   int
}

var _ CredentialStore = (*MemoryCredentialStore)(nil)

// NewMemoryCredentialStore returns an empty in-memory credential fake.
func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{}
}

// SetCredentials seeds the in-memory credential record.
func (s *MemoryCredentialStore) SetCredentials(record CredentialRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record = record.Clone()
	s.present = true
}

// SetErrors configures errors returned by load, save, and delete operations.
func (s *MemoryCredentialStore) SetErrors(loadErr, saveErr, deleteErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadErr = loadErr
	s.saveErr = saveErr
	s.deleteErr = deleteErr
}

// SavedRecords returns snapshots of records passed to SaveCredentials.
func (s *MemoryCredentialStore) SavedRecords() []CredentialRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCredentialRecords(s.saves)
}

// DeleteCount returns the number of successful delete calls.
func (s *MemoryCredentialStore) DeleteCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deletes
}

func (s *MemoryCredentialStore) LoadCredentials(ctx context.Context) (CredentialRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CredentialRecord{}, false, err
	}
	if s == nil {
		return CredentialRecord{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.loadErr != nil {
		return CredentialRecord{}, false, s.loadErr
	}
	return s.record.Clone(), s.present, nil
}

func (s *MemoryCredentialStore) SaveCredentials(ctx context.Context, record CredentialRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.record = record.Clone()
	s.present = true
	s.saves = append(s.saves, record.Clone())
	return nil
}

func (s *MemoryCredentialStore) DeleteCredentials(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.record = CredentialRecord{}
	s.present = false
	s.deletes++
	return nil
}

// FailingCredentialStore is a fake CredentialStore that returns Err from every
// operation after honoring context cancellation.
type FailingCredentialStore struct {
	Err error
}

var _ CredentialStore = FailingCredentialStore{}

func (s FailingCredentialStore) LoadCredentials(ctx context.Context) (CredentialRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CredentialRecord{}, false, err
	}
	return CredentialRecord{}, false, s.err()
}

func (s FailingCredentialStore) SaveCredentials(ctx context.Context, _ CredentialRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.err()
}

func (s FailingCredentialStore) DeleteCredentials(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.err()
}

func (s FailingCredentialStore) err() error {
	if s.Err != nil {
		return s.Err
	}
	return errors.New("credential store unavailable")
}

type credentialFileRecord struct {
	Version int                  `json:"version"`
	Twitch  credentialFileTwitch `json:"twitch"`
}

type credentialFileTwitch struct {
	UserID       string   `json:"user_id,omitempty"`
	Login        string   `json:"login,omitempty"`
	DisplayName  string   `json:"display_name,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	AccessToken  string   `json:"access_token,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

func credentialFileFromRecord(record CredentialRecord) credentialFileRecord {
	return credentialFileRecord{
		Version: CredentialFileRecordVersion,
		Twitch: credentialFileTwitch{
			UserID:       record.UserID,
			Login:        record.Login,
			DisplayName:  record.DisplayName,
			ClientID:     record.ClientID,
			AccessToken:  record.AccessToken.Reveal(),
			RefreshToken: record.RefreshToken.Reveal(),
			TokenType:    record.TokenType,
			Scopes:       auth.ScopeValues(record.Scopes),
			ExpiresAt:    formatCredentialTime(record.ExpiresAt),
			UpdatedAt:    formatCredentialTime(record.UpdatedAt),
		},
	}
}

func (f credentialFileRecord) toRecord() (CredentialRecord, error) {
	expiresAt, err := parseCredentialTime("expires_at", f.Twitch.ExpiresAt)
	if err != nil {
		return CredentialRecord{}, err
	}
	updatedAt, err := parseCredentialTime("updated_at", f.Twitch.UpdatedAt)
	if err != nil {
		return CredentialRecord{}, err
	}
	return CredentialRecord{
		UserID:       f.Twitch.UserID,
		Login:        f.Twitch.Login,
		DisplayName:  f.Twitch.DisplayName,
		ClientID:     f.Twitch.ClientID,
		AccessToken:  auth.NewSecret(f.Twitch.AccessToken),
		RefreshToken: auth.NewSecret(f.Twitch.RefreshToken),
		TokenType:    f.Twitch.TokenType,
		Scopes:       auth.Scopes(f.Twitch.Scopes...),
		ExpiresAt:    expiresAt,
		UpdatedAt:    updatedAt,
	}, nil
}

func formatCredentialTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseCredentialTime(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid credential %s", field)
	}
	return parsed, nil
}

func validateCredentialDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	return validateCredentialDirectoryInfo(info)
}

func ensureCredentialDirectory(dir string) error {
	created := false
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, CredentialDirectoryMode); err != nil {
			return err
		}
		created = true
	} else if err != nil {
		return err
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if created {
		if err := validateCredentialDirectoryInfo(info); err != nil {
			return err
		}
		if err := os.Chmod(dir, CredentialDirectoryMode); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
		if err != nil {
			return err
		}
	}
	return validateCredentialDirectoryInfo(info)
}

func validateCredentialDirectoryInfo(info fs.FileInfo) error {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: credential directory is a symlink", ErrInsecureCredentialPermissions)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: credential directory is not a directory", ErrInsecureCredentialPermissions)
	}
	return ValidateCredentialDirectoryMode(mode)
}

func validateCredentialReplacementTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return validateCredentialFileInfo(info)
}

func validateCredentialFileInfo(info fs.FileInfo) error {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: credential file is a symlink", ErrInsecureCredentialPermissions)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("%w: credential file is not a regular file", ErrInsecureCredentialPermissions)
	}
	return ValidateCredentialFileMode(mode)
}

func writeCredentialFileAtomically(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	closeFile := func() error {
		if file == nil {
			return nil
		}
		err := file.Close()
		file = nil
		return err
	}

	if err := file.Chmod(mode); err != nil {
		_ = closeFile()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = closeFile()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = closeFile()
		return err
	}
	if err := closeFile(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCredentialTempFile(tmpPath, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	if err := syncCredentialDirectory(dir); err != nil {
		return err
	}
	return nil
}

func validateCredentialTempFile(path string, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validateCredentialFileInfo(info); err != nil {
		return err
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("%w: credential temp file mode %s", ErrInsecureCredentialPermissions, info.Mode().Perm())
	}
	return nil
}

func syncCredentialDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

type credentialStoreError struct {
	message string
	matches []error
}

func (e credentialStoreError) Error() string {
	return e.message
}

func (e credentialStoreError) Is(target error) bool {
	for _, match := range e.matches {
		if errors.Is(match, target) {
			return true
		}
	}
	return false
}

func credentialFileOperationError(action, path string, err error, redactor auth.Redactor) error {
	if err == nil {
		return nil
	}
	return credentialStoreError{
		message: sanitizedCredentialMessage(action, path, err.Error(), redactor),
		matches: credentialErrorMatches(err),
	}
}

func credentialFileMalformedError(action, path string, err error) error {
	matches := []error{ErrMalformedCredentialFile}
	if errors.Is(err, ErrUnsupportedCredentialFileFormat) {
		matches = append(matches, ErrUnsupportedCredentialFileFormat)
	}
	return credentialStoreError{
		message: sanitizedCredentialMessage(action, path, ErrMalformedCredentialFile.Error(), auth.Redactor{}),
		matches: matches,
	}
}

func sanitizedCredentialMessage(action, path, detail string, redactor auth.Redactor) string {
	var builder strings.Builder
	builder.WriteString(action)
	if strings.TrimSpace(path) != "" {
		builder.WriteString(" ")
		builder.WriteString(path)
	}
	if strings.TrimSpace(detail) != "" {
		builder.WriteString(": ")
		builder.WriteString(detail)
	}
	message := builder.String()
	message = redactor.Redact(message)
	return auth.NewRedactor().Redact(message)
}

func credentialErrorMatches(err error) []error {
	var matches []error
	for _, candidate := range []error{
		ErrInsecureCredentialPermissions,
		ErrUnsupportedCredentialFilePlatform,
		ErrUnsupportedCredentialFileFormat,
		ErrMalformedCredentialFile,
		fs.ErrPermission,
		fs.ErrExist,
		fs.ErrNotExist,
		ErrPathIsDirectory,
	} {
		if errors.Is(err, candidate) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

type credentialFilePlatform struct {
	Supported bool
	Reason    string
	Action    string
}

func (p credentialFilePlatform) validate() error {
	if p.Supported {
		return nil
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		reason = "credential-file fallback is disabled on this platform"
	}
	action := strings.TrimSpace(p.Action)
	if action != "" {
		reason += "; " + action
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedCredentialFilePlatform, reason)
}

func cloneCredentialScopes(scopes []auth.Scope) []auth.Scope {
	if len(scopes) == 0 {
		return nil
	}
	return append([]auth.Scope(nil), scopes...)
}

func cloneCredentialRecords(records []CredentialRecord) []CredentialRecord {
	if len(records) == 0 {
		return nil
	}
	clones := make([]CredentialRecord, len(records))
	for i, record := range records {
		clones[i] = record.Clone()
	}
	return clones
}

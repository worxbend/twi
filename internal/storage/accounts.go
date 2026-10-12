package storage

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ValidateAccountName accepts an explicit local credential-store name, never a
// path. Names are case-sensitive and do not imply a Twitch login or theme.
func ValidateAccountName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("account name must contain 1 to 64 ASCII letters, digits, hyphens, or underscores")
	}
	for i, r := range name {
		alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alpha && (i == 0 || r != '_' && r != '-') {
			return errors.New("invalid account name")
		}
	}
	return nil
}

func namedAccountDirectory() (string, error) {
	path, err := DefaultCredentialFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "accounts"), nil
}

// NewNamedCredentialStore uses the existing private-file persistence contract.
// Explicit selection never falls back to the default store or environment token.
func NewNamedCredentialStore(name string) (*CredentialFileStore, error) {
	if err := ValidateAccountName(name); err != nil {
		return nil, err
	}
	dir, err := namedAccountDirectory()
	if err != nil {
		return nil, err
	}
	plan, err := NewCredentialFilePlan(filepath.Join(dir, name+".json"))
	if err != nil {
		return nil, err
	}
	return NewCredentialFileStore(plan)
}

// ListNamedAccounts lists only regular account records under a validated private
// directory. It never reads or emits token material and rejects symlink entries.
func ListNamedAccounts() ([]string, error) {
	dir, err := namedAccountDirectory()
	if err != nil {
		return nil, err
	}
	if err = validateCredentialDirectory(dir); errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if ValidateAccountName(name) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return nil, ErrInsecureCredentialPermissions
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

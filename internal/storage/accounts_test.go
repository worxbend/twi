package storage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/worxbend/twi/internal/auth"
)

func TestNamedAccountPrivateStore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := NewNamedCredentialStore("work")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(context.Background(), CredentialRecord{Login: "viewer", AccessToken: auth.NewSecret("fake-account-token")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	parent, err := os.Stat(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if parent.Mode().Perm() != 0700 {
		t.Fatalf("directory mode=%v", parent.Mode())
	}
	names, err := ListNamedAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"work"}) {
		t.Fatalf("names=%v", names)
	}
	if err := os.Symlink(store.Path(), filepath.Join(filepath.Dir(store.Path()), "symlink.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ListNamedAccounts(); err == nil {
		t.Fatal("symlink account accepted")
	}
}
func TestAccountNamesCannotEscapePrivateDirectory(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", ".", "..", "../default", "/tmp/credentials", "a.b", "a/b", "work space", "α", string(make([]byte, 65))} {
		if err := ValidateAccountName(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

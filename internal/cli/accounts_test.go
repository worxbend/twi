package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
)

func TestExplicitAccountOverridesDefaultCredentials(t *testing.T) {
	t.Parallel()
	r := newRunner()
	store := storage.NewMemoryCredentialStore()
	if err := store.SaveCredentials(context.Background(), storage.CredentialRecord{Login: "named", ClientID: "named-client", AccessToken: auth.NewSecret("named-access"), RefreshToken: auth.NewSecret("named-refresh")}); err != nil {
		t.Fatal(err)
	}
	r.newNamedCredentialStore = func(name string) (storage.CredentialStore, error) {
		if name != "work" {
			t.Fatalf("account=%s", name)
		}
		return store, nil
	}
	r.newCredentialStore = func() (storage.CredentialStore, error) {
		t.Fatal("explicit account fell back to default credentials")
		return nil, nil
	}
	if err := r.selectAccount("work"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Twitch = config.TwitchConfig{Username: "default", ClientID: "default-client", OAuthToken: "default-access", RefreshToken: "default-refresh", ClientSecret: "secret"}
	status, err := r.applyStoredCredentials(context.Background(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Twitch.Username != "named" || cfg.Twitch.ClientID != "named-client" || !strings.Contains(cfg.Twitch.OAuthToken, "named-access") || cfg.Twitch.RefreshToken != "named-refresh" || status.AccessTokenShadowed {
		t.Fatalf("explicit account did not determine credentials: %s", cfg.RedactedString())
	}
}
func TestMissingExplicitAccountCannotUseEnvironmentToken(t *testing.T) {
	t.Parallel()
	r := newRunner()
	r.newNamedCredentialStore = func(string) (storage.CredentialStore, error) { return storage.NewMemoryCredentialStore(), nil }
	if err := r.selectAccount("missing"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Twitch.OAuthToken = "environment-access"
	if _, err := r.applyStoredCredentials(context.Background(), &cfg); err == nil {
		t.Fatal("missing account silently fell back")
	}
}

func TestExplicitAccountCannotIgnoreUnavailableStore(t *testing.T) {
	t.Parallel()
	for _, storeErr := range []error{nil, storage.ErrUnsupportedCredentialFilePlatform} {
		r := newRunner()
		r.newNamedCredentialStore = func(string) (storage.CredentialStore, error) { return nil, storeErr }
		if err := r.selectAccount("work"); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.Twitch.OAuthToken = "environment-access"
		if _, err := r.applyStoredCredentials(context.Background(), &cfg); err == nil {
			t.Fatal("explicit account ignored unavailable store")
		}
	}
}

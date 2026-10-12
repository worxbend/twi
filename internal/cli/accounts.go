package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
)

func (r *runner) credentialStore() (storage.CredentialStore, error) {
	if r.account != "" {
		return r.newNamedCredentialStore(r.account)
	}
	return r.newCredentialStore()
}

func (r *runner) selectAccount(name string) error {
	if name != "" {
		if err := storage.ValidateAccountName(name); err != nil {
			return err
		}
	}
	r.account = name
	return nil
}

func (r *runner) runAccount(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, config.RedactDisplayValue(err.Error())); return 7 }
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: twi account list | show NAME | remove NAME\nSave credentials with: twi login --account NAME")
		return 0
	}
	if len(args) == 1 && args[0] == "list" {
		names, err := storage.ListNamedAccounts()
		if err != nil {
			return fail(err)
		}
		for _, name := range names {
			if _, err := fmt.Fprintln(stdout, name); err != nil {
				return 7
			}
		}
		return 0
	}
	if len(args) != 2 || (args[0] != "show" && args[0] != "remove") {
		fmt.Fprintln(stderr, "Usage: twi account list | show NAME | remove NAME")
		return 2
	}
	if err := r.selectAccount(args[1]); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	store, err := r.credentialStore()
	if err != nil {
		return fail(err)
	}
	if args[0] == "remove" {
		if err := store.DeleteCredentials(ctx); err != nil {
			return fail(err)
		}
		return 0
	}
	record, present, err := store.LoadCredentials(ctx)
	if err != nil {
		return fail(err)
	}
	if !present {
		return fail(errors.New("named account has no saved credentials"))
	}
	// Account inspection emits identity and granted capabilities, never secrets.
	if err := json.NewEncoder(stdout).Encode(struct {
		Name, Login, UserID, ClientID string
		Scopes                        []string
	}{Name: r.account, Login: record.Login, UserID: record.UserID, ClientID: record.ClientID, Scopes: accountScopes(record)}); err != nil {
		return 7
	}
	return 0
}
func accountScopes(record storage.CredentialRecord) []string {
	scopes := make([]string, len(record.Scopes))
	for i, s := range record.Scopes {
		scopes[i] = string(s)
	}
	return scopes
}

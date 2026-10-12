package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/storage"
)

func (r *runner) deviceLoginAndSave(parent context.Context, cfg config.Config, opts loginOptions, stdout, stderr io.Writer) int {
	request := auth.LoginRequest{ClientID: strings.TrimSpace(cfg.Twitch.ClientID), ClientSecret: auth.NewSecret(cfg.Twitch.ClientSecret), Scopes: loginCapabilityScopes(opts.capability)}
	if request.ClientID == "" {
		fmt.Fprintln(stderr, "device login requires TWITCH_CLIENT_ID")
		return 2
	}
	store, err := r.credentialStore()
	if err != nil || store == nil {
		fmt.Fprintln(stderr, "device login: private credential storage unavailable")
		return 7
	}
	ctx, cancel := context.WithTimeout(parent, opts.timeout)
	defer cancel()
	flow := r.newDeviceLoginFlow()
	challenge, err := flow.BeginDevice(ctx, request)
	if err != nil {
		printLoginError(stderr, "start device login", err, request.Redactor())
		return 1
	}
	// This challenge is intentionally shown to the authorizing user. The private
	// device code and tokens never reach stdout or debug logging.
	if _, err := fmt.Fprintf(stdout, "Authorize Twitch at %s\nUser code: %s\nRequested scopes: %s\nWaiting for consent; tokens will be saved privately.\n", challenge.VerificationURI, challenge.UserCode.Reveal(), strings.Join(auth.ScopeValues(request.Scopes), ", ")); err != nil {
		fmt.Fprintln(stderr, "device login: cannot write authorization challenge")
		return 7
	}
	result, err := flow.WaitDevice(ctx, request, challenge)
	redactor := request.Redactor().With(challenge.DeviceCode, challenge.UserCode)
	if err != nil {
		printLoginError(stderr, "complete device login", err, redactor)
		return 1
	}
	redactor = redactor.With(result.Tokens.AccessToken, result.Tokens.RefreshToken)
	if err := store.SaveCredentials(ctx, storage.CredentialRecordFromLoginResult(result, request.ClientID, time.Now().UTC())); err != nil {
		printLoginError(stderr, "save device credentials", err, redactor)
		return 7
	}
	printLoginSuccess(stdout, result, redactor, r.account)
	return 0
}

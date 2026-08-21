package helix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/worxbend/twi/internal/twitch"
)

const defaultOAuthValidateURL = "https://id.twitch.tv/oauth2/validate"

// OAuthTokenValidatorConfig configures the Twitch OAuth token validation HTTP
// adapter. Zero values use Twitch's production validation endpoint and the
// default HTTP client.
type OAuthTokenValidatorConfig struct {
	Endpoint   string
	HTTPClient *http.Client
	Now        func() time.Time
}

// OAuthTokenValidator validates Twitch access tokens through Twitch's OAuth
// validation endpoint.
type OAuthTokenValidator struct {
	endpoint   string
	httpClient *http.Client
	now        func() time.Time
}

var _ twitch.TokenValidator = (*OAuthTokenValidator)(nil)

// NewOAuthTokenValidator creates a TokenValidator backed by Twitch OAuth HTTP
// validation. The returned validator does not perform network I/O until
// ValidateToken is called.
func NewOAuthTokenValidator(cfg OAuthTokenValidatorConfig) *OAuthTokenValidator {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultOAuthValidateURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &OAuthTokenValidator{
		endpoint:   endpoint,
		httpClient: httpClient,
		now:        now,
	}
}

// ValidateToken validates credentials through Twitch OAuth and maps the
// response onto the shared token validation model without exposing credential
// values in returned errors.
func (v *OAuthTokenValidator) ValidateToken(ctx context.Context, credentials twitch.TokenCredentials) (twitch.TokenValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return twitch.TokenValidationResult{}, err
	}

	token := accessTokenForValidation(credentials.OAuthToken)
	if token == "" {
		return twitch.TokenValidationResult{
			Status:           twitch.TokenValidationMalformed,
			RefreshAvailable: credentials.RefreshAvailable(),
			Detail:           "missing OAuth token",
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return twitch.TokenValidationResult{}, credentialSafeError("create Twitch OAuth validation request", err, credentials)
	}
	req.Header.Set("Authorization", "OAuth "+token)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return twitch.TokenValidationResult{}, credentialSafeError("validate Twitch OAuth token", err, credentials)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		detail, err := decodeOAuthValidateError(resp.Body)
		if err != nil {
			return twitch.TokenValidationResult{}, credentialSafeError("read Twitch OAuth validation response", err, credentials)
		}
		if detail == "" {
			detail = "Twitch rejected the OAuth token"
		}
		status := twitch.TokenValidationMalformed
		if strings.Contains(strings.ToLower(detail), "expired") {
			status = twitch.TokenValidationExpired
		}
		return twitch.TokenValidationResult{
			Status:           status,
			RefreshAvailable: credentials.RefreshAvailable(),
			Detail:           redactCredentials(detail, credentials),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := readSmallBody(resp.Body)
		if err != nil {
			return twitch.TokenValidationResult{}, credentialSafeError("read Twitch OAuth validation response", err, credentials)
		}
		if detail != "" {
			detail = ": " + detail
		}
		return twitch.TokenValidationResult{}, credentialSafeError(
			"validate Twitch OAuth token",
			fmt.Errorf("twitch OAuth validation returned HTTP %d%s", resp.StatusCode, detail),
			credentials,
		)
	}

	var decoded oauthValidateResponse
	if err := decodeJSONBody(resp.Body, maxResponseBodySize, &decoded); err != nil {
		return twitch.TokenValidationResult{}, credentialSafeError("decode Twitch OAuth validation response", err, credentials)
	}
	return validationResultFromOAuthResponse(decoded, credentials, v.now()), nil
}

type oauthValidateResponse struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	Scopes    []string `json:"scopes"`
	UserID    string   `json:"user_id"`
	ExpiresIn int64    `json:"expires_in"`
}

type oauthValidateErrorResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func validationResultFromOAuthResponse(resp oauthValidateResponse, credentials twitch.TokenCredentials, now time.Time) twitch.TokenValidationResult {
	scopes := twitch.TokenScopes(resp.Scopes...)
	missing := twitch.MissingRequiredIRCScopes(scopes)
	expiresAt := now.Add(time.Duration(resp.ExpiresIn) * time.Second)
	result := twitch.TokenValidationResult{
		Status: twitch.TokenValidationValid,
		Identity: twitch.TokenIdentity{
			UserID: resp.UserID,
			Login:  strings.TrimSpace(resp.Login),
		},
		Scopes:           scopes,
		MissingScopes:    missing,
		ExpiresAt:        expiresAt,
		RefreshAvailable: credentials.RefreshAvailable(),
	}

	switch {
	case strings.TrimSpace(resp.Login) == "" || strings.TrimSpace(resp.UserID) == "":
		result.Status = twitch.TokenValidationMalformed
		result.Detail = "Twitch OAuth validation response did not include a user identity"
	case resp.ExpiresIn <= 0:
		result.Status = twitch.TokenValidationExpired
		result.Detail = "OAuth token expired"
	case twitch.LoginMismatch(credentials.Username, resp.Login):
		result.Status = twitch.TokenValidationWrongUser
		result.Detail = fmt.Sprintf("OAuth token belongs to Twitch user %q, not configured username %q", resp.Login, strings.TrimSpace(credentials.Username))
	case len(missing) > 0:
		result.Status = twitch.TokenValidationMissingScope
	}

	return result
}

func decodeOAuthValidateError(body io.Reader) (string, error) {
	data, err := readSmallBodyBytes(body)
	if err != nil {
		return "", err
	}
	var decoded oauthValidateErrorResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", nil
	}
	return strings.TrimSpace(decoded.Message), nil
}

func readSmallBody(body io.Reader) (string, error) {
	data, err := readSmallBodyBytes(body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readSmallBodyBytes(body io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, 4096))
}

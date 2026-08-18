package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const defaultOAuthValidateURL = "https://id.twitch.tv/oauth2/validate"

var (
	credentialValuePattern = regexp.MustCompile(`(?i)(oauth:|bearer\s+)[^\s"'&?]+`)
	credentialPairPattern  = regexp.MustCompile(`(?i)((?:client[_-]secret|refresh_token|authorization_code|code)(?:["']?\s*[:=]\s*["']?))[^"'\s&?]+`)
)

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

var _ TokenValidator = (*OAuthTokenValidator)(nil)

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
func (v *OAuthTokenValidator) ValidateToken(ctx context.Context, credentials TokenCredentials) (TokenValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return TokenValidationResult{}, err
	}

	token := accessTokenForValidation(credentials.OAuthToken)
	if token == "" {
		return TokenValidationResult{
			Status:           TokenValidationMalformed,
			RefreshAvailable: credentials.RefreshAvailable(),
			Detail:           "missing OAuth token",
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return TokenValidationResult{}, credentialSafeError("create Twitch OAuth validation request", err, credentials)
	}
	req.Header.Set("Authorization", "OAuth "+token)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return TokenValidationResult{}, credentialSafeError("validate Twitch OAuth token", err, credentials)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		detail, err := decodeOAuthValidateError(resp.Body)
		if err != nil {
			return TokenValidationResult{}, credentialSafeError("read Twitch OAuth validation response", err, credentials)
		}
		if detail == "" {
			detail = "Twitch rejected the OAuth token"
		}
		status := TokenValidationMalformed
		if strings.Contains(strings.ToLower(detail), "expired") {
			status = TokenValidationExpired
		}
		return TokenValidationResult{
			Status:           status,
			RefreshAvailable: credentials.RefreshAvailable(),
			Detail:           redactCredentials(detail, credentials),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, err := readSmallBody(resp.Body)
		if err != nil {
			return TokenValidationResult{}, credentialSafeError("read Twitch OAuth validation response", err, credentials)
		}
		if detail != "" {
			detail = ": " + detail
		}
		return TokenValidationResult{}, credentialSafeError(
			"validate Twitch OAuth token",
			fmt.Errorf("twitch OAuth validation returned HTTP %d%s", resp.StatusCode, detail),
			credentials,
		)
	}

	var decoded oauthValidateResponse
	if err := decodeJSONBody(resp.Body, maxHelixResponseBodySize, &decoded); err != nil {
		return TokenValidationResult{}, credentialSafeError("decode Twitch OAuth validation response", err, credentials)
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

func validationResultFromOAuthResponse(resp oauthValidateResponse, credentials TokenCredentials, now time.Time) TokenValidationResult {
	scopes := TokenScopes(resp.Scopes...)
	missing := MissingRequiredIRCScopes(scopes)
	expiresAt := now.Add(time.Duration(resp.ExpiresIn) * time.Second)
	result := TokenValidationResult{
		Status: TokenValidationValid,
		Identity: TokenIdentity{
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
		result.Status = TokenValidationMalformed
		result.Detail = "Twitch OAuth validation response did not include a user identity"
	case resp.ExpiresIn <= 0:
		result.Status = TokenValidationExpired
		result.Detail = "OAuth token expired"
	case usernameMismatch(credentials.Username, resp.Login):
		result.Status = TokenValidationWrongUser
		result.Detail = fmt.Sprintf("OAuth token belongs to Twitch user %q, not configured username %q", resp.Login, strings.TrimSpace(credentials.Username))
	case len(missing) > 0:
		result.Status = TokenValidationMissingScope
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

func accessTokenForValidation(value string) string {
	value = strings.TrimSpace(value)
	if prefix, body, ok := strings.Cut(value, ":"); ok && strings.EqualFold(prefix, "oauth") {
		return strings.TrimSpace(body)
	}
	return value
}

func usernameMismatch(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	return expected != "" && actual != "" && !strings.EqualFold(expected, actual)
}

func credentialSafeError(action string, err error, credentials TokenCredentials) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(action + ": " + redactCredentials(err.Error(), credentials))
}

func redactCredentials(value string, credentials TokenCredentials) string {
	value = credentialValuePattern.ReplaceAllString(value, "${1}<redacted>")
	value = credentialPairPattern.ReplaceAllString(value, "${1}<redacted>")
	for _, secret := range credentialSecrets(credentials) {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
}

func credentialSecrets(credentials TokenCredentials) []string {
	values := []string{
		strings.TrimSpace(credentials.OAuthToken),
		strings.TrimSpace(accessTokenForValidation(credentials.OAuthToken)),
		strings.TrimSpace(credentials.RefreshToken),
		strings.TrimSpace(credentials.ClientSecret),
	}
	seen := make(map[string]bool, len(values))
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		secrets = append(secrets, value)
	}
	return secrets
}

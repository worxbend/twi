package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DeviceLoginFlow obtains user consent without a browser on the local machine.
type DeviceLoginFlow interface {
	BeginDevice(context.Context, LoginRequest) (DeviceChallenge, error)
	WaitDevice(context.Context, LoginRequest, DeviceChallenge) (LoginResult, error)
}

type DeviceChallenge struct {
	DeviceCode      Secret
	UserCode        Secret
	VerificationURI string
	ExpiresAt       time.Time
	Interval        time.Duration
}

type DeviceLoginConfig struct {
	OAuth          TwitchOAuthLoginFlowConfig
	DeviceEndpoint string
	Wait           func(context.Context, time.Duration) error
}

type TwitchDeviceLogin struct {
	oauth    *TwitchOAuthLoginFlow
	endpoint string
	wait     func(context.Context, time.Duration) error
}

func NewTwitchDeviceLogin(cfg DeviceLoginConfig) *TwitchDeviceLogin {
	endpoint := cfg.DeviceEndpoint
	if endpoint == "" {
		endpoint = "https://id.twitch.tv/oauth2/device"
	}
	wait := cfg.Wait
	if wait == nil {
		wait = func(ctx context.Context, interval time.Duration) error {
			timer := time.NewTimer(interval)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &TwitchDeviceLogin{oauth: NewTwitchOAuthLoginFlow(cfg.OAuth), endpoint: endpoint, wait: wait}
}

func (f *TwitchDeviceLogin) post(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, f.oauth.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, errors.New("invalid device authorization endpoint")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *f.oauth.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, errors.New("device authorization request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthResponseBodySize+1))
	if err != nil || len(data) > maxOAuthResponseBodySize {
		return 0, nil, errors.New("invalid device authorization response")
	}
	return response.StatusCode, data, nil
}

func (f *TwitchDeviceLogin) BeginDevice(ctx context.Context, request LoginRequest) (DeviceChallenge, error) {
	if strings.TrimSpace(request.ClientID) == "" {
		return DeviceChallenge{}, errors.New("device login requires a Twitch client ID")
	}
	status, data, err := f.post(ctx, f.endpoint, url.Values{"client_id": {request.ClientID}, "scopes": {strings.Join(ScopeValues(request.RequiredScopes()), " ")}})
	if err != nil {
		return DeviceChallenge{}, err
	}
	if status != http.StatusOK {
		return DeviceChallenge{}, errors.New("start device login: Twitch returned HTTP " + strconv.Itoa(status))
	}
	var decoded struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        int64  `json:"interval"`
	}
	if json.Unmarshal(data, &decoded) != nil || decoded.DeviceCode == "" || decoded.UserCode == "" || decoded.ExpiresIn <= 0 || decoded.ExpiresIn > 86400 || decoded.Interval < 1 || decoded.Interval > 60 {
		return DeviceChallenge{}, errors.New("invalid device authorization challenge")
	}
	if len(decoded.UserCode) < 4 || len(decoded.UserCode) > 32 {
		return DeviceChallenge{}, errors.New("invalid device user code")
	}
	for _, character := range decoded.UserCode {
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
			return DeviceChallenge{}, errors.New("invalid device user code")
		}
	}
	verification, err := url.Parse(decoded.VerificationURI)
	if err != nil || verification.Scheme != "https" || verification.Host != "www.twitch.tv" || verification.Path != "/activate" || verification.User != nil || verification.Fragment != "" {
		return DeviceChallenge{}, errors.New("invalid Twitch device verification URL")
	}
	// Only the intended user-facing code may appear in the verification URL.
	query, err := url.ParseQuery(verification.RawQuery)
	if err != nil {
		return DeviceChallenge{}, errors.New("invalid device verification query")
	}
	for key, values := range query {
		if key != "public" && key != "device-code" || len(values) != 1 {
			return DeviceChallenge{}, errors.New("unexpected device verification URL parameter")
		}
		if key == "device-code" && values[0] != decoded.UserCode {
			return DeviceChallenge{}, errors.New("device verification code mismatch")
		}
		if key == "public" && values[0] != "true" && values[0] != "false" {
			return DeviceChallenge{}, errors.New("invalid public device verification parameter")
		}
	}
	if decoded.UserCode == decoded.DeviceCode || strings.Contains(verification.String(), decoded.DeviceCode) {
		return DeviceChallenge{}, errors.New("private device code appeared in public verification URL")
	}
	return DeviceChallenge{DeviceCode: NewSecret(decoded.DeviceCode), UserCode: NewSecret(decoded.UserCode), VerificationURI: verification.String(), ExpiresAt: f.oauth.now().Add(time.Duration(decoded.ExpiresIn) * time.Second), Interval: time.Duration(decoded.Interval) * time.Second}, nil
}

func (f *TwitchDeviceLogin) WaitDevice(parent context.Context, request LoginRequest, challenge DeviceChallenge) (LoginResult, error) {
	if !challenge.DeviceCode.Present() || challenge.Interval < time.Second || challenge.Interval > time.Minute || !challenge.ExpiresAt.After(f.oauth.now()) {
		return LoginResult{}, errors.New("invalid or expired device authorization challenge")
	}
	ctx, cancel := context.WithDeadline(parent, challenge.ExpiresAt)
	defer cancel()
	interval := challenge.Interval
	form := url.Values{"client_id": {request.ClientID}, "device_code": {challenge.DeviceCode.Reveal()}, "scopes": {strings.Join(ScopeValues(request.RequiredScopes()), " ")}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	if request.ClientSecret.Present() {
		form.Set("client_secret", request.ClientSecret.Reveal())
	}
	for {
		if err := f.wait(ctx, interval); err != nil {
			return LoginResult{}, err
		}
		status, data, err := f.post(ctx, f.oauth.tokenEndpoint, form)
		if err != nil {
			return LoginResult{}, err
		}
		if status != http.StatusOK {
			var failure oauthErrorResponse
			_ = json.Unmarshal(data, &failure)
			code := failure.Error
			if code == "" {
				code = failure.Message
			}
			if status == http.StatusBadRequest {
				switch code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval = min(time.Minute, interval+5*time.Second)
					continue
				case "access_denied":
					return LoginResult{}, errors.New("device authorization denied by user")
				case "expired_token", "invalid device code":
					return LoginResult{}, errors.New("device authorization expired; restart login")
				}
			}
			return LoginResult{}, errors.New("device token exchange: Twitch returned HTTP " + strconv.Itoa(status))
		}
		var decoded oauthTokenResponse
		if json.Unmarshal(data, &decoded) != nil || decoded.AccessToken == "" || decoded.RefreshToken == "" || !strings.EqualFold(decoded.TokenType, "bearer") || decoded.ExpiresIn <= 0 || decoded.ExpiresIn > 86400 {
			return LoginResult{}, errors.New("invalid device token response")
		}
		access, refresh := NewSecret(decoded.AccessToken), NewSecret(decoded.RefreshToken)
		validationCtx, stop := context.WithTimeout(ctx, f.oauth.requestTimeout)
		validated, err := f.oauth.validateAccessToken(validationCtx, access, NewRedactor(request.ClientSecret, challenge.DeviceCode, challenge.UserCode, access, refresh))
		stop()
		if err != nil {
			return LoginResult{}, err
		}
		if validated.ClientID != request.ClientID {
			return LoginResult{}, errors.New("device token belongs to another Twitch client")
		}
		if missing := MissingScopes(validated.Scopes, request.RequiredScopes()); len(missing) > 0 {
			return LoginResult{}, missingScopesError("device token", missing, request.RequiredScopes())
		}
		expires := minTime(validated.ExpiresAt, f.oauth.now().Add(time.Duration(decoded.ExpiresIn)*time.Second))
		scopes := cloneScopes(validated.Scopes)
		return LoginResult{Identity: validated.Identity, Scopes: scopes, Tokens: TokenSet{AccessToken: access, RefreshToken: refresh, TokenType: decoded.TokenType, ExpiresAt: expires, Scopes: cloneScopes(scopes)}}, nil
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

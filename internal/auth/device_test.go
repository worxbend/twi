package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginPendingSlowDownAndValidatedIdentity(t *testing.T) {
	var waits []time.Duration
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "client" || r.Form.Get("scopes") != "chat:read" {
				t.Error("incorrect device request")
			}
			fmt.Fprint(w, `{"device_code":"private-device","user_code":"USERCODE","verification_uri":"https://www.twitch.tv/activate?device-code=USERCODE","expires_in":120,"interval":5}`)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("device_code") != "private-device" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("scopes") != "chat:read" {
				t.Error("incorrect device token request")
			}
			polls++
			switch polls {
			case 1:
				w.WriteHeader(400)
				fmt.Fprint(w, `{"message":"authorization_pending"}`)
			case 2:
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"slow_down"}`)
			default:
				fmt.Fprint(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"bearer","expires_in":3600,"scope":["chat:read"]}`)
			}
		case "/validate":
			if r.Header.Get("Authorization") != "OAuth access-secret" {
				t.Error("wrong validated token")
			}
			fmt.Fprint(w, `{"client_id":"client","user_id":"123","login":"owner","scopes":["chat:read"],"expires_in":3000}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	flow := NewTwitchDeviceLogin(DeviceLoginConfig{DeviceEndpoint: server.URL + "/device", OAuth: TwitchOAuthLoginFlowConfig{TokenEndpoint: server.URL + "/token", ValidateEndpoint: server.URL + "/validate"}, Wait: func(ctx context.Context, interval time.Duration) error {
		waits = append(waits, interval)
		return ctx.Err()
	}})
	request := LoginRequest{ClientID: "client", Scopes: []Scope{ScopeChatRead}}
	challenge, err := flow.BeginDevice(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.WaitDevice(context.Background(), request, challenge)
	if err != nil || result.Identity.Login != "owner" || !result.Tokens.RefreshAvailable() {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if !reflect.DeepEqual(waits, []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}) {
		t.Fatal("incorrect polling pace", waits)
	}
	if strings.Contains(fmt.Sprint(challenge, result), "private-device") || strings.Contains(fmt.Sprint(result), "access-secret") {
		t.Fatal("secret formatting leaked")
	}
}

func TestDeviceChallengeRejectsUnsafeVerificationAndBody(t *testing.T) {
	for _, verification := range []string{"https://attacker.example/activate", "https://www.twitch.tv/activate?device_code=private-device", "https://www.twitch.tv/activate?device-code=OTHER", "https://www.twitch.tv/activate?public=true&public=false", "https://www.twitch.tv/activate?public=private-device", "https://www.twitch.tv/activate?public=%ZZ%70rivate-device", "http://www.twitch.tv/activate"} {
		t.Run(verification, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "private-device", "user_code": "USERCODE", "verification_uri": verification, "expires_in": 120, "interval": 5})
			}))
			defer server.Close()
			flow := NewTwitchDeviceLogin(DeviceLoginConfig{DeviceEndpoint: server.URL})
			if _, err := flow.BeginDevice(context.Background(), LoginRequest{ClientID: "client"}); err == nil || strings.Contains(err.Error(), "private-device") {
				t.Fatal("unsafe verification accepted or leaked")
			}
		})
	}
}

func TestDeviceLoginCancellationAndDeniedErrorsStayPrivate(t *testing.T) {
	for _, response := range []string{`{"message":"access_denied"}`, `{"message":"private-device access-secret client-secret"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); fmt.Fprint(w, response) }))
		flow := NewTwitchDeviceLogin(DeviceLoginConfig{OAuth: TwitchOAuthLoginFlowConfig{TokenEndpoint: server.URL}, Wait: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }})
		challenge := DeviceChallenge{DeviceCode: NewSecret("private-device"), Interval: time.Second, ExpiresAt: time.Now().Add(time.Minute)}
		_, err := flow.WaitDevice(context.Background(), LoginRequest{ClientID: "client", ClientSecret: NewSecret("client-secret")}, challenge)
		if err == nil || strings.Contains(err.Error(), "private-device") || strings.Contains(err.Error(), "client-secret") {
			t.Fatal("unsafe exchange failure", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := flow.WaitDevice(ctx, LoginRequest{ClientID: "client"}, challenge); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		server.Close()
	}
}

func TestDeviceLoginDoesNotForwardSecretToRedirect(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	flow := NewTwitchDeviceLogin(DeviceLoginConfig{OAuth: TwitchOAuthLoginFlowConfig{TokenEndpoint: server.URL}, Wait: func(context.Context, time.Duration) error { return nil }})
	_, err := flow.WaitDevice(context.Background(), LoginRequest{ClientID: "client", ClientSecret: NewSecret("client-secret")}, DeviceChallenge{DeviceCode: NewSecret("device-secret"), Interval: time.Second, ExpiresAt: time.Now().Add(time.Minute)})
	if err == nil || forwarded {
		t.Fatal("device grant followed redirect", err)
	}
}

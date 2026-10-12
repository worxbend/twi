package helix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/worxbend/twi/internal/auth"
)

func TestAppTokensAreClientCredentialsAndRejectUserTokenValidation(t *testing.T) {
	t.Parallel()
	userToken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "client" || r.Form.Get("client_secret") != "fake-secret" {
				t.Error("incorrect client-credentials request")
			}
			_, _ = w.Write([]byte(`{"access_token":"fake-app","token_type":"bearer","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "OAuth fake-app" {
			t.Error("incorrect validation authorization")
		}
		if userToken {
			_, _ = w.Write([]byte(`{"client_id":"client","user_id":"unexpected-user","login":"user","expires_in":3600}`))
		} else {
			_, _ = w.Write([]byte(`{"client_id":"client","expires_in":3600}`))
		}
	}))
	defer server.Close()
	client := NewAppTokenClient(AppTokenConfig{ClientID: "client", ClientSecret: auth.NewSecret("fake-secret"), TokenURL: server.URL + "/token", ValidateURL: server.URL + "/validate"})
	token, _, err := client.Acquire(context.Background())
	if err != nil || !token.Present() {
		t.Fatal(err)
	}
	userToken = true
	if _, _, err := client.Acquire(context.Background()); err == nil || strings.Contains(err.Error(), "fake-secret") || strings.Contains(err.Error(), "fake-app") {
		t.Fatalf("user token accepted or secrets leaked: %v", err)
	}
}
func TestAppGrantLookupIsExplicitAndBounded(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()["user_id"]) != 2 || r.Header.Get("Authorization") != "Bearer app-token" {
			t.Error("grant identity request lost")
		}
		_, _ = w.Write([]byte(`{"data":[{"user_id":"sender","has_authorized":true,"scopes":["user:write:chat","user:bot"]},{"user_id":"broadcaster","has_authorized":false,"scopes":[]}]}`))
	}))
	defer server.Close()
	client := NewAuthorizationClient(ClientConfig{Endpoint: server.URL, OAuthToken: "app-token"})
	grants, err := client.GetGrants(context.Background(), []string{"sender", "broadcaster"})
	if err != nil || len(grants) != 2 || grants[1].Authorized {
		t.Fatal(grants, err)
	}
	if _, err := client.GetGrants(context.Background(), make([]string, 11)); err == nil {
		t.Fatal("unbounded grant query")
	}
}

func TestAppTokenExchangeNeverForwardsSecretsThroughRedirect(t *testing.T) {
	t.Parallel()
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
		t.Error("app credentials forwarded to redirect target")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	provided := &http.Client{}
	client := NewAppTokenClient(AppTokenConfig{HTTPClient: provided, ClientID: "client", ClientSecret: auth.NewSecret("fake-redirect-secret"), TokenURL: redirect.URL})
	if _, _, err := client.Acquire(context.Background()); err == nil || strings.Contains(err.Error(), "fake-redirect-secret") {
		t.Fatalf("redirect succeeded or leaked secret: %v", err)
	}
	if forwarded {
		t.Fatal("credential endpoint redirect was followed")
	}
	if provided.CheckRedirect != nil {
		t.Fatal("constructor mutated shared caller client")
	}
}

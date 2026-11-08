package openaiauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/zalando/go-keyring"
)

type oauthFixture struct {
	client                        *oauthClient
	mu                            sync.Mutex
	params                        url.Values
	key                           *rsa.PrivateKey
	mutate                        func(map[string]any)
	grantScope                    string
	exchanges, refreshes, revoked int
	invalidGrant                  bool
	callbackClient                string
}

func fixture(t *testing.T) *oauthFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oauthFixture{key: key, grantScope: scopes, callbackClient: "oaiapp_test"}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.client = &oauthClient{http: srv.Client(), issuer: srv.URL, authorize: srv.URL + "/authorize", token: srv.URL + "/token", discovery: srv.URL + "/openid", keys: &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}}
	mux.HandleFunc("/openid", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "revocation_endpoint": srv.URL + "/revoke"})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("token") == "" || r.Form.Get("client_id") != "oaiapp_test" {
			t.Error("bad revocation")
		}
		f.revoked++
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.exchanges++
		if r.Form.Get("client_id") != "oaiapp_test" || r.Form.Get("resource") != resource || r.Form.Has("client_secret") {
			t.Error("wrong token audience/client")
		}
		if f.invalidGrant {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant","error_description":"secret not for the UI"}`)
			return
		}
		if r.Form.Get("grant_type") == "authorization_code" {
			h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(h[:]) != f.params.Get("code_challenge") || r.Form.Get("redirect_uri") != f.params.Get("redirect_uri") {
				t.Error("PKCE or redirect mismatch")
			}
		} else {
			f.refreshes++
			if r.Form.Get("refresh_token") == "" || r.Form.Has("scope") {
				t.Error("invalid refresh contract")
			}
		}
		claims := map[string]any{"iss": srv.URL, "aud": "oaiapp_test", "sub": "test-account", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": f.params.Get("nonce"), "email": "synthetic@example.test"}
		if f.mutate != nil {
			f.mutate(claims)
		}
		body, _ := json.Marshal(claims)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		unsigned := header + "." + base64.RawURLEncoding.EncodeToString(body)
		hash := sha256.Sum256([]byte(unsigned))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		id := unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access", "refresh_token": "rotating-refresh", "id_token": id, "token_type": "Bearer", "scope": f.grantScope, "expires_in": 3600})
	})
	return f
}
func (f *oauthFixture) browser(t *testing.T) func(string) error {
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		f.mu.Lock()
		f.params = q
		f.mu.Unlock()
		if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" || q.Get("ext_agent_host_id") == "" {
			t.Error("missing login security fields")
		}
		cb, _ := url.Parse(q.Get("redirect_uri"))
		if cb.Hostname() != "127.0.0.1" || cb.Path != "/auth/callback" {
			t.Error("invalid loopback")
		}
		// A stray/forged callback must not consume the pending attempt.
		cb.RawQuery = url.Values{"state": {"forged"}, "code": {"no"}}.Encode()
		res, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Error("forged state accepted")
		}
		cb.RawQuery = url.Values{"state": {q.Get("state")}, "code": {"synthetic-code"}, "client_id": {f.callbackClient}}.Encode()
		res, err = http.Get(cb.String())
		if err == nil {
			res.Body.Close()
		}
		return err
	}
}
func testManager(t *testing.T, f *oauthFixture) *Manager {
	s := newDiskStorage(t.TempDir())
	key := ""
	s.getKey = func() (string, error) {
		if key == "" {
			return "", keyring.ErrNotFound
		}
		return key, nil
	}
	s.setKey = func(s string) error { key = s; return nil }
	return &Manager{storage: s, oauth: f.client}
}
func settled(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OAuth did not settle")
	}
}
func TestLoginEncryptedPersistenceRefreshAndSignout(t *testing.T) {
	f := fixture(t)
	m := testManager(t, f)
	ctx := context.Background()
	if err := m.SignIn(ctx, "", f.browser(t)); err != nil {
		t.Fatal(err)
	}
	settled(t, m)
	v := m.Snapshot()
	if !v.Connected || !v.PlanEnabled || v.Running {
		t.Fatal(v)
	}
	c, err := m.storage.load()
	if err != nil {
		t.Fatal(err)
	}
	host := c.HostID
	b, _ := os.ReadFile(filepath.Join(m.storage.dir, "openai.enc"))
	if strings.Contains(string(b), "test-access") || strings.Contains(string(b), "synthetic@example.test") {
		t.Fatal("plaintext credential storage")
	}
	encoded, _ := json.Marshal(v)
	for _, secret := range []string{"test-access", "rotating-refresh", "id_token", "synthetic-code"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret in snapshot")
		}
	}
	c.Accounts[0].ExpiresAt = time.Now().Add(-time.Minute)
	if err = m.storage.save(c); err != nil {
		t.Fatal(err)
	}
	token, err := m.AccessToken(ctx)
	if err != nil || token != "test-access" {
		t.Fatal("refresh failed", err)
	}
	if err = m.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	settled(t, m)
	v = m.Snapshot()
	if v.Connected {
		t.Fatal(v)
	}
	c, err = m.storage.load()
	if err != nil || c.HostID != host || len(c.Accounts) != 1 || c.Accounts[0].RefreshToken != "" || c.Accounts[0].IDToken != "" {
		t.Fatal("logout lost registration or retained tokens", err)
	}
	f.mu.Lock()
	if f.refreshes != 1 || f.revoked != 1 {
		t.Error("refresh or revocation missing")
	}
	f.mu.Unlock()
	// Returning sign-in reuses the registration and host, with no dynamic hint.
	if err = m.SignIn(ctx, c.Accounts[0].ClientID, f.browser(t)); err != nil {
		t.Fatal(err)
	}
	settled(t, m)
	f.mu.Lock()
	if f.params.Get("client_id") != "oaiapp_test" || f.params.Has("agent_name_hint") || f.params.Get("ext_agent_host_id") != host || f.params.Has("id_token_hint") {
		t.Error("returning registration was not reused")
	}
	f.mu.Unlock()
}
func TestRejectsUntrustedIdentityAndKeepsRegistration(t *testing.T) {
	for _, field := range []string{"nonce", "iss", "aud", "exp", "sub", "signature"} {
		t.Run(field, func(t *testing.T) {
			f := fixture(t)
			f.mutate = func(claims map[string]any) {
				if field == "exp" {
					claims[field] = time.Now().Add(-time.Hour).Unix()
				} else if field == "sub" {
					claims[field] = ""
				} else {
					claims[field] = "wrong"
				}
			}
			if field == "signature" {
				other, _ := rsa.GenerateKey(rand.Reader, 2048)
				f.client.keys = &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&other.PublicKey}}
			}
			m := testManager(t, f)
			m.SignIn(context.Background(), "", f.browser(t))
			settled(t, m)
			if m.Snapshot().Connected {
				t.Fatal("unverified identity accepted")
			}
			c, err := m.storage.load()
			if err != nil || len(c.Accounts) != 1 || c.Accounts[0].ClientID != "oaiapp_test" || c.Accounts[0].AccessToken != "" {
				t.Fatal("registration lost or invalid tokens saved", err)
			}
		})
	}
}
func TestCancellationGrantFailureScopeAndAccountMismatch(t *testing.T) {
	f := fixture(t)
	m := testManager(t, f)
	ctx := context.Background()
	opened := make(chan struct{})
	m.SignIn(ctx, "", func(string) error { close(opened); return nil })
	<-opened
	m.Cancel()
	settled(t, m)
	if m.Snapshot().Connected || !strings.Contains(m.Snapshot().Message, "cancelled") {
		t.Fatal(m.Snapshot())
	}
	f.invalidGrant = true
	m.SignIn(ctx, "", f.browser(t))
	settled(t, m)
	c, err := m.storage.load()
	if err != nil || len(c.Accounts) != 1 || m.Snapshot().Connected || strings.Contains(m.Snapshot().Message, "secret not") {
		t.Fatal("invalid grant handling", err)
	}
	f.invalidGrant = false
	f.grantScope = "openid email"
	m.SignIn(ctx, "oaiapp_test", f.browser(t))
	settled(t, m)
	if !m.Snapshot().Connected || m.Snapshot().PlanEnabled {
		t.Fatal("identity-only login treated as usage grant")
	}
	if _, err = m.AccessToken(ctx); err == nil {
		t.Fatal("identity-only token used for AI")
	}
	f.mutate = func(c map[string]any) { c["sub"] = "different-person" }
	m.SignIn(ctx, "oaiapp_test", f.browser(t))
	settled(t, m)
	c, _ = m.storage.load()
	if c.Accounts[0].Subject != "test-account" {
		t.Fatal("returning account identity replaced")
	}
	f.callbackClient = "oaiapp_wrong"
	m.SignIn(ctx, "oaiapp_test", f.browser(t))
	settled(t, m)
	if !strings.Contains(m.Snapshot().Message, "different account registration") {
		t.Fatal(m.Snapshot())
	}
}
func TestCredentialStoreFailureStopsBeforeBrowserAndTamperingFails(t *testing.T) {
	f := fixture(t)
	m := testManager(t, f)
	m.storage.setKey = func(string) error { return errors.New("unavailable") }
	m.SignIn(context.Background(), "", func(string) error { t.Error("browser opened before secure storage available"); return nil })
	settled(t, m)
	if !strings.Contains(m.Snapshot().Message, "credential store") {
		t.Fatal(m.Snapshot())
	}
	m = testManager(t, f)
	if err := m.storage.save(credentials{HostID: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.storage.dir, "openai.enc")
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 1
	os.WriteFile(path, b, 0600)
	if _, err := m.storage.load(); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestUnconfirmedRevocationAndInstanceLock(t *testing.T) {
	f := fixture(t)
	m := testManager(t, f)
	ctx := context.Background()
	opened := make(chan struct{})
	m.SignIn(ctx, "", func(string) error { close(opened); return nil })
	<-opened
	second := &Manager{storage: m.storage, oauth: f.client}
	second.SignIn(ctx, "", func(string) error { t.Error("concurrent login opened browser"); return nil })
	settled(t, second)
	if !strings.Contains(second.Snapshot().Message, "Another Nekonomicon") {
		t.Fatal(second.Snapshot())
	}
	m.Cancel()
	settled(t, m)
	m.SignIn(ctx, "", f.browser(t))
	settled(t, m)
	// A malformed discovery response cannot redirect a refresh token elsewhere.
	f.client.discovery = f.client.issuer + "/missing"
	m.SignOut(ctx)
	settled(t, m)
	if m.Snapshot().Connected || !strings.Contains(m.Snapshot().Message, "revocation was not confirmed") {
		t.Fatal(m.Snapshot())
	}
	c, err := m.storage.load()
	if err != nil || c.Accounts[0].AccessToken != "" || c.Accounts[0].RefreshToken != "" {
		t.Fatal("local signout failed", err)
	}
}

package openaiauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const issuer = "https://auth.openai.com"
const resource = "https://api.openai.com/v1"
const dynamicClient = "dynamic_agent_client"
const scopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

type oauthClient struct {
	http                                *http.Client
	issuer, authorize, token, discovery string
	keys                                oidc.KeySet
}

type boundedTransport struct{ base http.RoundTripper }
type boundedBody struct {
	io.Reader
	io.Closer
}

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err == nil {
		// Also bound discovery/JWKS reads performed by the OIDC verifier.
		res.Body = boundedBody{io.LimitReader(res.Body, 1<<20), res.Body}
	}
	return res, err
}

func newOAuthClient() *oauthClient {
	c := &http.Client{Transport: boundedTransport{http.DefaultTransport}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &oauthClient{http: c, issuer: issuer, authorize: issuer + "/api/accounts/authorize", token: issuer + "/api/accounts/oauth/token", discovery: issuer + "/.well-known/openid-configuration", keys: oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), c), issuer+"/.well-known/jwks.json")}
}
func randomValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hasScope(scopes, scope string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == scope {
			return true
		}
	}
	return false
}
func planEnabled(a account) bool {
	return a.AccessToken != "" && hasScope(a.Scope, "resource.invoke") && hasScope(a.Scope, "chatgpt.tokens.use.direct")
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

var errReauthorize = errors.New("OpenAI session expired or was revoked. Continue with ChatGPT to sign in again")

func (c *oauthClient) exchange(ctx context.Context, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.token, strings.NewReader(form.Encode()))
	if err != nil {
		return t, errors.New("Could not prepare OpenAI sign-in")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return t, errors.New("Could not reach OpenAI. Check your connection and retry")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, (128<<10)+1))
	if err != nil || len(b) > 128<<10 {
		return t, errors.New("OpenAI returned an invalid token response")
	}
	if res.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &problem)
		if problem.Error == "invalid_grant" {
			return t, errReauthorize
		}
		return t, errors.New("OpenAI did not complete sign-in. Retry or check this app’s access in ChatGPT Settings")
	}
	if json.Unmarshal(b, &t) != nil || !strings.EqualFold(t.TokenType, "Bearer") || t.AccessToken == "" || t.ExpiresIn <= 0 || t.ExpiresIn > 86400 {
		return t, errors.New("OpenAI returned an invalid token response")
	}
	return t, nil
}
func (c *oauthClient) identity(ctx context.Context, raw, clientID, nonce string) (account, error) {
	var a account
	v := oidc.NewVerifier(c.issuer, c.keys, &oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{"RS256"}})
	id, err := v.Verify(ctx, raw)
	if err != nil || id.Subject == "" || nonce != "" && subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(nonce)) != 1 {
		return a, errors.New("OpenAI identity could not be verified. Retry sign-in")
	}
	var claims struct {
		Email, Name     string
		AuthorizedParty string `json:"azp"`
	}
	if id.Claims(&claims) != nil || claims.AuthorizedParty != "" && claims.AuthorizedParty != clientID {
		return a, errors.New("OpenAI identity could not be verified. Retry sign-in")
	}
	a.ClientID, a.Subject, a.Email, a.Name = clientID, id.Subject, claims.Email, claims.Name
	return a, nil
}

// login owns one loopback listener and accepts only the matching, one-use state.
// Registration is saved before exchange so an expired code can be retried with
// its issued client ID instead of creating duplicate registrations.
func (c *oauthClient) login(ctx context.Context, host string, selected account, openBrowser func(string) error, register func(string) error) (account, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return account{}, errors.New("Could not open the local OpenAI sign-in callback")
	}
	defer l.Close()
	redirect := "http://" + l.Addr().String() + "/auth/callback"
	state, nonce, verifier := randomValue(), randomValue(), randomValue()
	digest := sha256.Sum256([]byte(verifier))
	clientID := selected.ClientID
	if clientID == "" {
		clientID = dynamicClient
	}
	params := url.Values{"client_id": {clientID}, "ext_agent_host_id": {host}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {scopes}, "resource": {resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
	if clientID == dynamicClient {
		params.Set("agent_name_hint", "Nekonomicon")
	} else {
		if selected.IDToken != "" {
			params.Set("id_token_hint", selected.IDToken)
		}
		if selected.Email != "" {
			params.Set("login_hint", selected.Email)
		}
	}
	callback := make(chan url.Values, 1)
	var received atomic.Bool
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		q, parseErr := url.ParseQuery(r.URL.RawQuery)
		if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" || r.Host != l.Addr().String() || len(r.URL.RawQuery) > 16384 || parseErr != nil || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid sign-in callback. Return to Nekonomicon.", 400)
			return
		}
		for _, key := range []string{"code", "client_id", "error"} {
			if len(q[key]) > 1 {
				http.Error(w, "Invalid sign-in callback.", 400)
				return
			}
		}
		if !received.CompareAndSwap(false, true) {
			http.Error(w, "This sign-in response was already received.", 409)
			return
		}
		select {
		case callback <- q:
			io.WriteString(w, "Sign-in response received. Return to Nekonomicon to check the connection.")
		default:
			http.Error(w, "This sign-in response was already received.", 409)
		}
	})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(l) }()
	defer server.Close()
	if err = openBrowser(c.authorize + "?" + params.Encode()); err != nil {
		return account{}, errors.New("Could not open the browser for OpenAI sign-in")
	}
	var q url.Values
	select {
	case <-ctx.Done():
		return account{}, ctx.Err()
	case <-serveDone:
		return account{}, errors.New("OpenAI sign-in listener stopped. Retry sign-in")
	case q = <-callback:
	}
	if q.Get("error") != "" {
		return account{}, errors.New("OpenAI sign-in was not authorized. You can try again")
	}
	issued := q.Get("client_id")
	if clientID == dynamicClient {
		if issued == "" || issued == dynamicClient || len(issued) > 256 || strings.ContainsAny(issued, "\r\n\t ") {
			return account{}, errors.New("OpenAI did not return a valid app registration")
		}
		if err = register(issued); err != nil {
			return account{}, err
		}
		clientID = issued
	} else if issued != "" && issued != clientID {
		return account{}, errors.New("OpenAI returned a different account registration. Sign-in was rejected")
	}
	if q.Get("code") == "" {
		return account{}, errors.New("OpenAI did not return a sign-in code")
	}
	t, err := c.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {q.Get("code")}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {resource}})
	if err != nil {
		return account{}, err
	}
	a, err := c.identity(ctx, t.IDToken, clientID, nonce)
	if err != nil {
		return account{}, err
	}
	if selected.Subject != "" && selected.Subject != a.Subject {
		return account{}, errors.New("OpenAI returned a different account. Add it as a new account instead")
	}
	a.AccessToken, a.RefreshToken, a.IDToken, a.Scope, a.ExpiresAt = t.AccessToken, t.RefreshToken, t.IDToken, t.Scope, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second)
	return a, nil
}

func (c *oauthClient) refresh(ctx context.Context, a account) (account, error) {
	if a.RefreshToken == "" {
		return a, errReauthorize
	}
	t, err := c.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {a.ClientID}, "refresh_token": {a.RefreshToken}, "resource": {resource}})
	if err != nil {
		return a, err
	}
	if t.RefreshToken == "" {
		return a, errors.New("OpenAI did not renew the session. Sign in again")
	}
	if t.IDToken != "" {
		identity, err := c.identity(ctx, t.IDToken, a.ClientID, "")
		if err != nil {
			return a, err
		}
		if identity.Subject != a.Subject {
			return a, errors.New("OpenAI returned a different account during renewal")
		}
		a.IDToken = t.IDToken
	}
	a.AccessToken, a.RefreshToken, a.ExpiresAt = t.AccessToken, t.RefreshToken, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second)
	if t.Scope != "" {
		a.Scope = t.Scope
	}
	return a, nil
}
func (c *oauthClient) revoke(ctx context.Context, a account) bool {
	if a.RefreshToken == "" {
		return a.AccessToken == ""
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.discovery, nil)
	res, err := c.http.Do(req)
	if err != nil {
		return false
	}
	var discovery struct {
		Issuer     string `json:"issuer"`
		Revocation string `json:"revocation_endpoint"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&discovery)
	res.Body.Close()
	u, parseErr := url.Parse(discovery.Revocation)
	base, _ := url.Parse(c.issuer)
	if res.StatusCode != 200 || decodeErr != nil || parseErr != nil || discovery.Issuer != c.issuer || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.RawQuery != "" {
		return false
	}
	form := url.Values{"token": {a.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {a.ClientID}}
	for attempt := 0; attempt < 2; attempt++ {
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err = c.http.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return true
			}
			if res.StatusCode < 500 {
				return false
			}
		}
		if attempt == 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return false
}

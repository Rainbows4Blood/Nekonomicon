package openaiauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

// View deliberately contains no tokens, authorization URLs, or raw errors.
type AccountView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type View struct {
	Running     bool          `json:"running"`
	State       string        `json:"state"`
	Message     string        `json:"message"`
	Active      string        `json:"active"`
	Accounts    []AccountView `json:"accounts"`
	Connected   bool          `json:"connected"`
	PlanEnabled bool          `json:"planEnabled"`
}
type Manager struct {
	mu      sync.Mutex
	storage *diskStorage
	oauth   *oauthClient
	v       View
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
}

func New(ctx context.Context, dir string) *Manager {
	m := &Manager{storage: newDiskStorage(dir), oauth: newOAuthClient()}
	_ = m.start(ctx, "loading", func(context.Context, *credentials) error { return nil })
	return m
}
func (m *Manager) Snapshot() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.v
	v.Accounts = append([]AccountView{}, v.Accounts...)
	return v
}
func (m *Manager) start(parent context.Context, state string, work func(context.Context, *credentials) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("The app is closing")
	}
	if m.v.Running {
		return errors.New("Wait for the current OpenAI connection action")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	m.cancel = cancel
	m.done = make(chan struct{})
	m.v.Running, m.v.State, m.v.Message = true, state, map[string]string{"loading": "Checking saved OpenAI connection…", "signing_in": "Continue in your browser. Sign-in expires after five minutes.", "signing_out": "Signing out of OpenAI…"}[state]
	go func() {
		defer cancel()
		c, err := m.withStorage(ctx, work)
		m.mu.Lock()
		defer m.mu.Unlock()
		if c != nil {
			m.v = snapshot(*c)
		}
		m.v.Running = false
		if err != nil {
			m.v.Message = err.Error()
			if errors.Is(err, context.Canceled) {
				m.v.Message = "OpenAI sign-in cancelled."
			}
			if errors.Is(err, context.DeadlineExceeded) {
				m.v.Message = "OpenAI sign-in timed out. Continue with ChatGPT to try again."
			}
		}
		if m.v.State == state {
			m.v.State = "disconnected"
		}
		close(m.done)
	}()
	return nil
}
func (m *Manager) withStorage(ctx context.Context, work func(context.Context, *credentials) error) (*credentials, error) {
	if m.storage.dir == "" {
		return nil, errors.New("OpenAI configuration folder is unavailable")
	}
	if err := os.MkdirAll(m.storage.dir, 0700); err != nil {
		return nil, errors.New("OpenAI configuration folder is unavailable")
	}
	// Serialize rotating tokens and credential writes even across app instances
	// using different catalogs. No lock or credential belongs in the catalog.
	lock := flock.New(filepath.Join(m.storage.dir, "openai.lock"))
	defer lock.Close()
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return nil, errors.New("Another Nekonomicon window is using the OpenAI connection. Finish there and retry")
	}
	c, err := m.storage.load()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err == nil {
		err = work(ctx, &c)
	}
	return &c, err
}
func snapshot(c credentials) View {
	v := View{State: "disconnected", Message: "Connect OpenAI for D&D adventure enrichment.", Accounts: []AccountView{}, Active: c.Active}
	for i, a := range c.Accounts {
		label := a.Email
		if label == "" {
			label = a.Name
		}
		if label == "" {
			label = "Pending sign-in"
		}
		v.Accounts = append(v.Accounts, AccountView{a.ClientID, fmt.Sprintf("%s · account %d", label, i+1)})
		if a.ClientID == c.Active && a.AccessToken != "" {
			v.Connected, v.PlanEnabled, v.State = true, planEnabled(a), "connected"
			v.Message = "Connected as " + label + ". Ready for D&D adventure enrichment."
			if !v.PlanEnabled {
				v.Message = "Signed in as " + label + ". ChatGPT plan access was not granted. Continue with ChatGPT to authorize it."
			}
		}
	}
	return v
}
func (m *Manager) SignIn(ctx context.Context, id string, openBrowser func(string) error) error {
	return m.start(ctx, "signing_in", func(ctx context.Context, c *credentials) error {
		selected := account{}
		index := -1
		for i, a := range c.Accounts {
			if a.ClientID == id {
				selected = a
				index = i
				break
			}
		}
		if id != "" && index < 0 {
			return errors.New("Choose a saved OpenAI account or add a new one")
		}
		if index < 0 && len(c.Accounts) >= 20 {
			return errors.New("This installation already has 20 OpenAI registrations")
		}
		if c.HostID == "" {
			c.HostID = "urn:uuid:" + uuid.NewString()
		}
		// Save host/key before opening the browser; unavailable keyrings must not
		// lead to a login that we cannot persist securely.
		if err := m.storage.save(*c); err != nil {
			return err
		}
		a, err := m.oauth.login(ctx, c.HostID, selected, openBrowser, func(issued string) error {
			for _, a := range c.Accounts {
				if a.ClientID == issued {
					return errors.New("This OpenAI registration is already saved. Select it and retry")
				}
			}
			c.Accounts = append(c.Accounts, account{ClientID: issued})
			index = len(c.Accounts) - 1
			return m.storage.save(*c)
		})
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		prior := *c
		prior.Accounts = append([]account{}, c.Accounts...)
		c.Accounts[index], c.Active = a, a.ClientID
		if err = m.storage.save(*c); err != nil {
			*c = prior
			return err
		}
		return nil
	})
}
func (m *Manager) SignOut(ctx context.Context) error {
	return m.start(ctx, "signing_out", func(ctx context.Context, c *credentials) error {
		for i, a := range c.Accounts {
			if a.ClientID == c.Active {
				remote := m.oauth.revoke(ctx, a)
				previous := a
				a.AccessToken, a.RefreshToken, a.IDToken, a.Scope = "", "", "", ""
				a.ExpiresAt = time.Time{}
				c.Accounts[i], c.Active = a, ""
				if err := m.storage.save(*c); err != nil {
					c.Accounts[i], c.Active = previous, previous.ClientID
					return errors.New("Could not remove the saved OpenAI session. Retry Sign out to clear local credentials")
				}
				if !remote {
					return errors.New("Signed out locally. Remote revocation was not confirmed; disconnect Nekonomicon in ChatGPT Settings")
				}
				return nil
			}
		}
		return nil
	})
}
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil && m.v.State == "signing_in" {
		m.cancel()
	}
}
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

// AccessToken is for future Go-side model calls only; Manager is never bound to
// Wails. Refresh is serialized with login/logout and across app instances.
// It performs no inference and enforces the separate plan-usage grant.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	if m.closed || m.v.Running {
		m.mu.Unlock()
		return "", errors.New("OpenAI connection is busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m.cancel, m.done = cancel, make(chan struct{})
	m.v.Running, m.v.State, m.v.Message = true, "refreshing", "Checking OpenAI session…"
	m.mu.Unlock()
	var token string
	c, err := m.withStorage(ctx, func(ctx context.Context, c *credentials) error {
		for i, a := range c.Accounts {
			if a.ClientID == c.Active && planEnabled(a) {
				if !a.ExpiresAt.After(time.Now().Add(time.Minute)) {
					refreshed, err := m.oauth.refresh(ctx, a)
					if err != nil {
						return err
					}
					c.Accounts[i] = refreshed
					if err = m.storage.save(*c); err != nil {
						return err
					}
					a = refreshed
				}
				if !planEnabled(a) {
					return errors.New("ChatGPT plan usage is not authorized")
				}
				token = a.AccessToken
				return nil
			}
		}
		return errors.New("Connect an OpenAI account with ChatGPT plan access first")
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if c != nil {
		m.v = snapshot(*c)
	}
	m.v.Running = false
	if err != nil {
		m.v.Message = err.Error()
	}
	close(m.done)
	return token, err
}

package main

import (
	"errors"

	"github.com/Rainbows4Blood/Nekonomicon/internal/openaiauth"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) openAIConnection() (*openaiauth.Manager, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enrichmentRunning() {
		return nil, errors.New("Stop enrichment before changing the OpenAI connection")
	}
	if a.closing || a.openAI == nil {
		return nil, errors.New("OpenAI connection is unavailable while the app is starting or closing")
	}
	return a.openAI, nil
}
func (a *App) SignInOpenAI(account string) error {
	m, err := a.openAIConnection()
	if err != nil {
		return err
	}
	return m.SignIn(a.ctx, account, func(url string) error { wruntime.BrowserOpenURL(a.ctx, url); return nil })
}
func (a *App) CancelOpenAISignIn() error {
	m, err := a.openAIConnection()
	if err == nil {
		m.Cancel()
	}
	return err
}
func (a *App) SignOutOpenAI() error {
	m, err := a.openAIConnection()
	if err != nil {
		return err
	}
	return m.SignOut(a.ctx)
}
func (a *App) OpenAISettings() { wruntime.BrowserOpenURL(a.ctx, "https://chatgpt.com/settings/usage") }

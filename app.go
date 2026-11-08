package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/engine"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichment"
	"github.com/Rainbows4Blood/Nekonomicon/internal/maintenance"
	"github.com/Rainbows4Blood/Nekonomicon/internal/openaiauth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

type App struct {
	enrichment                                    *enrichment.Runner
	openAI                                        *openaiauth.Manager
	source                                        storefront.Source
	mu                                            sync.Mutex
	ctx                                           context.Context
	cancel                                        context.CancelFunc
	engine                                        *engine.Engine
	maintenance                                   *maintenance.Manager
	store                                         *store.Store
	network                                       *dtrpg.Network
	vault                                         auth.Vault
	folder, storageError, authNote                string
	busy                                          bool
	loading, migrating, closing, shutdownComplete bool
	loadingMessage                                string
	loadCancel                                    context.CancelFunc
	loadDone                                      chan struct{}
	shutdownOnce                                  sync.Once
	eventsOnce                                    sync.Once
	openStore                                     func(context.Context, string, func(string)) (*store.Store, error)
	quit                                          func(context.Context)
}
type View struct {
	Enrichment       enrichment.Snapshot  `json:"enrichment"`
	OpenAI           openaiauth.View      `json:"openAI"`
	Maintenance      maintenance.Snapshot `json:"maintenance"`
	StorageAvailable bool                 `json:"storageAvailable"`
	LoadingError     string               `json:"loadingError"`
	Loading          bool                 `json:"loading"`
	Migrating        bool                 `json:"migrating"`
	Closing          bool                 `json:"closing"`
	LoadingMessage   string               `json:"loadingMessage"`
	Source           storefront.Source    `json:"source"`
	SourceName       string               `json:"sourceName"`
	WebsiteHost      string               `json:"websiteHost"`
	APIHost          string               `json:"apiHost"`
	ProbeID          int64                `json:"probeID"`
	engine.Snapshot
	DataFolder   string `json:"dataFolder"`
	StorageReady bool   `json:"storageReady"`
	Busy         bool   `json:"busy"`
	AuthNote     string `json:"authNote"`
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Nekonomicon")
}

// Sessions are isolated even though the stores share a protocol.
func session(source storefront.Source) (*dtrpg.Network, auth.Vault, string) {
	v := auth.Vault{Dir: configDir(), Source: source}
	cookies, err := v.Load()
	note := "Saved cookies loaded. Test the connection to verify access."
	if err != nil {
		note = "Sign in to " + source.Name() + " in Firefox, then import the matching profile/container or paste cookies."
	}
	return dtrpg.NewNetwork(cookies, nil, source), v, note
}

type preferences struct {
	DataFolder string            `json:"dataFolder"`
	Source     storefront.Source `json:"source"`
}

func savePreferences(folder string, source storefront.Source) error {
	b, err := json.Marshal(preferences{folder, source})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(configDir(), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(configDir(), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(configDir(), "settings.json"))
}

func (a *App) SelectSource(value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	source := storefront.Source(value)
	if !source.Valid() {
		return errors.New("Unknown storefront")
	}
	if a.busy || a.closing || a.maintenanceHeld() || a.engine != nil && (a.engine.Snapshot().Running || a.engine.Snapshot().Run != nil) {
		return errors.New("End the current run before changing storefront")
	}
	if source == a.source {
		return nil
	}
	n, vault, note := session(source)
	var e *engine.Engine
	var err error
	if a.store != nil && a.store.Version() == store.SchemaVersion {
		e, err = engine.New(a.store, n, engine.DefaultConfig())
		if err != nil {
			return err
		}
	}
	if err = savePreferences(a.folder, source); err != nil {
		return errors.New("Could not save the storefront preference")
	}
	a.source, a.network, a.vault, a.authNote, a.engine = source, n, vault, note, e
	return nil
}
func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			return filepath.Join(p, "Nekonomicon")
		}
	}
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "nekonomicon")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nekonomicon")
}
func (a *App) Snapshot() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := View{LoadingError: a.storageError, Loading: a.loading, Migrating: a.migrating, Closing: a.closing, LoadingMessage: a.loadingMessage, Source: a.source, SourceName: a.source.Name(), WebsiteHost: "www." + a.source.Domain(), APIHost: "api." + a.source.Domain(), ProbeID: a.source.ProbeID(), DataFolder: a.folder, StorageReady: a.engine != nil && !a.loading && !a.closing, Busy: a.busy || a.closing, AuthNote: a.authNote}
	if a.enrichment != nil {
		v.Enrichment = a.enrichment.Snapshot()
	}
	if a.openAI != nil {
		v.OpenAI = a.openAI.Snapshot()
	}
	v.StorageAvailable = a.store != nil && !a.loading && !a.closing
	if a.maintenance != nil {
		v.Maintenance = a.maintenance.Snapshot()
	}
	if a.engine != nil {
		v.Snapshot = a.engine.Snapshot()
	} else {
		v.State = "blocked_storage"
		v.Message = a.storageError
		if v.Maintenance.NeedsSchema {
			v.State = "maintenance_required"
			v.Message = v.Maintenance.Message
		}
	}
	if a.loading || a.closing {
		v.State = "loading"
		v.Message = a.loadingMessage
	}
	return v
}
func (a *App) Profiles() []auth.Profile { return auth.Profiles() }
func (a *App) Start(mode string, covers bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maintenanceHeld() {
		return errors.New("Finish or end data maintenance before crawling")
	}
	if a.busy || a.closing {
		return errors.New("Wait for the connection check to finish")
	}
	if a.engine == nil {
		return errors.New("Choose an available data folder first")
	}
	return a.engine.Start(mode, covers)
}
func (a *App) Resume() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maintenanceHeld() {
		return errors.New("Finish or end data maintenance before crawling")
	}
	if a.busy || a.closing {
		return errors.New("Wait for the connection check to finish")
	}
	if a.engine == nil {
		return errors.New("Storage unavailable")
	}
	return a.engine.Resume()
}
func (a *App) Pause() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading || a.closing {
		return errors.New("Wait for local storage to finish")
	}
	if a.engine == nil {
		return errors.New("Storage unavailable")
	}
	return a.engine.Pause()
}
func (a *App) End() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading || a.closing {
		return errors.New("Wait for local storage to finish")
	}
	if a.engine == nil {
		return errors.New("Storage unavailable")
	}
	return a.engine.End()
}
func (a *App) Failures(offset int) ([]store.Failure, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading || a.closing {
		return []store.Failure{}, nil
	}
	if a.engine == nil {
		return []store.Failure{}, nil
	}
	return a.engine.Failures(offset)
}
func (a *App) beginConnection() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy || a.closing || a.maintenanceHeld() {
		return errors.New("A connection operation is already in progress")
	}
	if a.engine != nil {
		if err := a.engine.SetReady(false); err != nil {
			return err
		}
	}
	a.busy = true
	return nil
}
func (a *App) finishConnection(note string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.busy = false
	a.authNote = note
}
func (a *App) saveCookies(c auth.Cookies, warnings []string) error {
	if err := a.vault.Save(c); err != nil {
		a.finishConnection("Could not save the session in private credential storage. Unlock your OS keyring and retry.")
		return errors.New("Private credential storage unavailable")
	}
	a.network.Replace(c)
	note := "Cookies imported. Test the connection to verify access."
	if len(warnings) > 0 {
		note += " " + strings.Join(warnings, " ")
	}
	a.finishConnection(note)
	return nil
}
func (a *App) ImportFirefox(index int, container uint32) error {
	if err := a.beginConnection(); err != nil {
		return err
	}
	profiles := auth.Profiles()
	if index < 0 || index >= len(profiles) {
		a.finishConnection("Select an available Firefox profile.")
		return errors.New("Firefox profile not found")
	}
	c, w, err := auth.Import(profiles[index], container, a.source)
	if err != nil {
		a.finishConnection(err.Error())
		return err
	}
	return a.saveCookies(c, w)
}
func (a *App) SaveCookies(website, api string) error {
	if err := a.beginConnection(); err != nil {
		return err
	}
	c, err := auth.Manual(website, api, a.source)
	if err != nil {
		a.finishConnection(err.Error())
		return err
	}
	return a.saveCookies(c, nil)
}
func (a *App) TestConnection(id int64) error {
	if id <= 0 {
		return errors.New("Enter a positive product ID")
	}
	if err := a.beginConnection(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()
	if err := a.network.Test(ctx, id); err != nil {
		message := dtrpg.FailureOf(err).Message
		a.finishConnection(message)
		return errors.New(message)
	}
	a.mu.Lock()
	if a.engine != nil {
		_ = a.engine.SetReady(true)
	}
	a.busy = false
	a.authNote = fmt.Sprintf("Website and product %d checked successfully. Connection ready.", id)
	a.mu.Unlock()
	return nil
}

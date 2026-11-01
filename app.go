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
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	mu                             sync.Mutex
	ctx                            context.Context
	cancel                         context.CancelFunc
	engine                         *engine.Engine
	store                          *store.Store
	network                        *dtrpg.Network
	vault                          auth.Vault
	folder, storageError, authNote string
	busy                           bool
}
type View struct {
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
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.vault = auth.Vault{Dir: configDir()}
	a.folder = defaultDataDir()
	var settings struct {
		DataFolder string `json:"dataFolder"`
	}
	if b, err := os.ReadFile(filepath.Join(configDir(), "settings.json")); err == nil && len(b) < 8192 && json.Unmarshal(b, &settings) == nil && filepath.IsAbs(settings.DataFolder) {
		a.folder = settings.DataFolder
	}
	cookies, err := a.vault.Load()
	if err != nil {
		a.authNote = "Import a Firefox session or paste cookies. Test the connection before crawling."
	} else {
		a.authNote = "Saved cookies loaded. Test the connection to verify access."
	}
	a.network = dtrpg.NewNetwork(cookies, nil)
	if err = a.open(a.folder); err != nil {
		a.storageError = "Local storage is unavailable or already in use. Choose another data folder or close the other instance."
	}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				wruntime.EventsEmit(a.ctx, "progress", a.Snapshot())
			}
		}
	}()
}
func (a *App) open(folder string) error {
	s, err := store.Open(folder)
	if err != nil {
		return err
	}
	e, err := engine.New(s, a.network, engine.DefaultConfig())
	if err != nil {
		s.Close()
		return err
	}
	a.store = s
	a.engine = e
	a.folder = s.Dir
	a.storageError = ""
	return nil
}
func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	e, s, cancel := a.engine, a.store, a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if e != nil {
		e.Shutdown()
	}
	if s != nil {
		s.Close()
	}
}
func (a *App) Snapshot() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := View{DataFolder: a.folder, StorageReady: a.engine != nil, Busy: a.busy, AuthNote: a.authNote}
	if a.engine != nil {
		v.Snapshot = a.engine.Snapshot()
	} else {
		v.State = "blocked_storage"
		v.Message = a.storageError
	}
	return v
}
func (a *App) Profiles() []auth.Profile { return auth.Profiles() }
func (a *App) Start(mode string, covers bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
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
	if a.busy {
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
	if a.engine == nil {
		return errors.New("Storage unavailable")
	}
	return a.engine.Pause()
}
func (a *App) End() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil {
		return errors.New("Storage unavailable")
	}
	return a.engine.End()
}
func (a *App) Failures(offset int) ([]store.Failure, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.engine == nil {
		return []store.Failure{}, nil
	}
	return a.engine.Failures(offset)
}
func (a *App) beginConnection() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
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
	c, w, err := auth.Import(profiles[index], container)
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
	c, err := auth.Manual(website, api)
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
func (a *App) ChooseDataFolder() error {
	a.mu.Lock()
	if a.busy || a.engine != nil && (a.engine.Snapshot().Running || a.engine.Snapshot().Run != nil) {
		a.mu.Unlock()
		return errors.New("End the current run before changing the data folder")
	}
	a.busy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.busy = false; a.mu.Unlock() }()
	folder, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose Nekonomicon data folder"})
	if err != nil {
		return errors.New("Could not open the folder chooser")
	}
	if folder == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if filepath.Clean(folder) == filepath.Clean(a.folder) && a.engine != nil {
		return nil
	}
	s, err := store.Open(folder)
	if err != nil {
		return errors.New("That folder is unavailable or already used by another instance")
	}
	e, err := engine.New(s, a.network, engine.DefaultConfig())
	if err != nil {
		s.Close()
		return errors.New("Could not open the catalog in that folder")
	}
	b, _ := json.Marshal(struct {
		DataFolder string `json:"dataFolder"`
	}{s.Dir})
	if err = os.MkdirAll(configDir(), 0700); err == nil {
		err = os.WriteFile(filepath.Join(configDir(), "settings.json"), b, 0600)
	}
	if err != nil {
		s.Close()
		return errors.New("Could not save the data folder preference")
	}
	ready := a.engine != nil && a.engine.Snapshot().ConnectionReady
	if a.store != nil {
		a.store.Close()
	}
	a.store = s
	a.engine = e
	a.folder = s.Dir
	a.storageError = ""
	_ = e.SetReady(ready)
	return nil
}

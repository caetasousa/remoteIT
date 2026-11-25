//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"remoteit/internal/agent/lifecycle"
	"remoteit/internal/agent/update"
	"remoteit/internal/dpapi"
	"remoteit/internal/viewer/client"
	"remoteit/internal/viewer/win"
)

func run() {
	dir := configDir()
	os.MkdirAll(dir, 0o700)
	if self, err := os.Executable(); err == nil {
		os.Remove(self + ".old") // sobra de uma atualização
	}
	logger := slog.New(slog.NewTextHandler(logFile(dir), nil))
	logger.Info("visualizador iniciado", "versao", version)

	settings := load(dir)
	if settings.Relay == "" {
		settings.Relay = relayURL
	}

	var once sync.Once
	pub, _ := base64.StdEncoding.DecodeString(updatePublicKey)
	err := win.Run(win.Options{
		Settings: settings,
		Version:  version,
		Save:     func(s win.Settings) { save(dir, s) },
		OnLogin: func(api *client.API) {
			once.Do(func() { // um atualizador só, mesmo com vários logins
				u := &update.Updater{
					Edition: "tecnico", Current: version, PublicKey: pub,
					Offer:    api.UpdateOffer,
					Download: api.DownloadUpdate,
					Busy:     win.Busy,
					Apply:    func(exe []byte) error { return lifecycle.ApplyUpdate(lifecycle.Portable, exe) },
					Logger:   logger,
				}
				go u.Run(context.Background())
			})
		},
	})
	if err != nil {
		logger.Error("visualizador encerrado com erro", "err", err)
		os.Exit(1)
	}
}

func configDir() string {
	if d, err := os.UserCacheDir(); err == nil { // %LOCALAPPDATA%
		return filepath.Join(d, "RemoteIT")
	}
	return filepath.Join(os.TempDir(), "RemoteIT")
}

func logFile(dir string) io.Writer {
	f, err := os.OpenFile(filepath.Join(dir, "viewer.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return io.Discard
	}
	return f
}

func load(dir string) win.Settings {
	var s win.Settings
	if b, err := os.ReadFile(filepath.Join(dir, "viewer.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "viewer.token")); err == nil {
		if t, err := dpapi.Unprotect(b, false); err == nil {
			s.Token = string(t)
		}
	}
	return s
}

func save(dir string, s win.Settings) {
	b, _ := json.Marshal(s)
	os.WriteFile(filepath.Join(dir, "viewer.json"), b, 0o600)
	tokenPath := filepath.Join(dir, "viewer.token")
	if s.Token == "" {
		os.Remove(tokenPath)
		return
	}
	if enc, err := dpapi.Protect([]byte(s.Token), false); err == nil {
		os.WriteFile(tokenPath, enc, 0o600)
	}
}

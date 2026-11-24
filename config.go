package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config holds user preferences, stored in the user config directory
// (Windows: %AppData%\Photo2GPX\config.json, macOS: ~/Library/Application Support/Photo2GPX/config.json).
type Config struct {
	Lang        string `json:"lang"`        // "" = not chosen yet (detected by the UI on first start)
	AutoUpdate  bool   `json:"autoUpdate"`  // check for updates at start-up
	SkipVersion string `json:"skipVersion"` // a release the user chose to skip
}

var cfgMu sync.Mutex

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Photo2GPX", "config.json")
}

func loadConfig() Config {
	c := Config{AutoUpdate: true}
	p := configPath()
	if p == "" {
		return c
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func saveConfig(c Config) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	p := configPath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}

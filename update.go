package main

// Update check and self-update from GitHub Releases.
//
// The release workflow (.github/workflows/release.yml) publishes these assets:
//   Photo2GPX-windows-amd64.exe     Windows program (used by the updater)
//   Photo2GPX-macos-universal.zip   macOS app bundle (used by the updater)
//   Photo2GPX-macos-universal.dmg   macOS installer for people
//   checksums.txt                   SHA-256 of every asset (verified before installing)

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var githubAPI = "https://api.github.com" // overridable for tests (P2G_GITHUB_API)

func init() {
	if v := os.Getenv("P2G_GITHUB_API"); v != "" {
		githubAPI = strings.TrimRight(v, "/")
	}
}

// UpdateInfo is returned to the UI.
type UpdateInfo struct {
	Available  bool   `json:"available"`
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Notes      string `json:"notes"`
	PageURL    string `json:"pageURL"`
	CanInstall bool   `json:"canInstall"` // this platform can update itself
	Error      string `json:"error"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	Tag     string    `json:"tag_name"`
	Body    string    `json:"body"`
	HTMLURL string    `json:"html_url"`
	Draft   bool      `json:"draft"`
	Pre     bool      `json:"prerelease"`
	Assets  []ghAsset `json:"assets"`
}

func updateAssetName() string {
	if v := os.Getenv("P2G_UPDATE_ASSET"); v != "" { // for testing on other systems
		return v
	}
	switch goruntime.GOOS {
	case "windows":
		return "Photo2GPX-windows-amd64.exe"
	case "darwin":
		return "Photo2GPX-macos-universal.zip"
	}
	return ""
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func repoConfigured() bool {
	return GitHubRepo != "" && !strings.HasPrefix(GitHubRepo, "OWNER/")
}

func fetchLatestRelease() (*ghRelease, error) {
	if !repoConfigured() {
		return nil, errors.New("update source not configured")
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/repos/%s/releases/latest", githubAPI, GitHubRepo), nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no release published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var r ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// parseVersion turns "v1.2.3" / "1.2" into comparable numbers.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out
}

// newerVersion reports whether a is newer than b.
func newerVersion(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return len(pa) > len(pb)
}

func (a *App) checkUpdate() (UpdateInfo, *ghRelease) {
	info := UpdateInfo{Current: AppVersion}
	rel, err := fetchLatestRelease()
	if err != nil {
		info.Error = err.Error()
		logger.Warn("update check failed", "err", info.Error)
		return info, nil
	}
	info.Latest = strings.TrimPrefix(rel.Tag, "v")
	info.Notes = rel.Body
	info.PageURL = rel.HTMLURL
	info.Available = !rel.Draft && !rel.Pre && newerVersion(rel.Tag, AppVersion)
	if name := updateAssetName(); name != "" {
		for _, as := range rel.Assets {
			if as.Name == name {
				info.CanInstall = true
			}
		}
	}
	logger.Info("update check", "current", AppVersion, "latest", info.Latest, "available", info.Available, "can_install", info.CanInstall)
	return info, rel
}

// CheckUpdate is the manual "Check for updates" action.
func (a *App) CheckUpdate() UpdateInfo {
	info, _ := a.checkUpdate()
	return info
}

// AutoCheckUpdate runs at start-up; it stays silent unless a new version
// exists, the user enabled automatic checks, and the version was not skipped.
func (a *App) AutoCheckUpdate() UpdateInfo {
	a.mu.Lock()
	enabled, skip := a.cfg.AutoUpdate, a.cfg.SkipVersion
	a.mu.Unlock()
	if !enabled || !repoConfigured() {
		return UpdateInfo{Current: AppVersion}
	}
	info, _ := a.checkUpdate()
	if info.Error != "" || (info.Available && info.Latest == skip) {
		return UpdateInfo{Current: AppVersion}
	}
	return info
}

// SkipUpdate remembers a version the user does not want to be offered again.
func (a *App) SkipUpdate(version string) {
	a.mu.Lock()
	a.cfg.SkipVersion = version
	c := a.cfg
	a.mu.Unlock()
	saveConfig(c)
}

// InstallUpdate downloads the latest release, verifies it, installs it and
// restarts the program. It returns an error message, or "" when the program
// is restarting.
func (a *App) InstallUpdate() string {
	msg := a.installUpdate()
	if msg != "" {
		logger.Error("update failed", "err", msg)
	} else {
		logger.Info("update installed, restarting")
	}
	return msg
}

func (a *App) installUpdate() string {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return "an update is already running"
	}
	a.updating = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.updating = false; a.mu.Unlock() }()

	info, rel := a.checkUpdate()
	if rel == nil {
		return info.Error
	}
	if !info.Available {
		return "already up to date"
	}
	name := updateAssetName()
	var asset *ghAsset
	var sums *ghAsset
	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case name:
			asset = &rel.Assets[i]
		case "checksums.txt":
			sums = &rel.Assets[i]
		}
	}
	if asset == nil {
		return "no download for this system in the release"
	}
	if sums == nil {
		return "checksums.txt missing from the release"
	}
	sumData, err := download(sums.URL, nil)
	if err != nil {
		return err.Error()
	}
	want := findChecksum(sumData, name)
	if want == "" {
		return "no checksum for " + name
	}
	data, err := download(asset.URL, func(done, total int64) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "update-progress", map[string]int64{"done": done, "total": total})
		}
	})
	if err != nil {
		return err.Error()
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != want {
		return "checksum mismatch, download corrupted"
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "update-progress", map[string]int64{"done": -1})
	}
	relaunch, err := applyUpdate(data)
	if err != nil {
		return err.Error()
	}
	if err := relaunch(); err != nil {
		return "installed, but could not restart: " + err.Error()
	}
	if a.ctx != nil {
		go func() {
			time.Sleep(300 * time.Millisecond)
			runtime.Quit(a.ctx)
		}()
	}
	return ""
}

func download(url string, progress func(done, total int64)) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	total := resp.ContentLength
	var buf bytes.Buffer
	chunk := make([]byte, 64*1024)
	var done int64
	last := time.Time{}
	for {
		n, err := resp.Body.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 150*time.Millisecond {
				progress(done, total)
				last = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if done > 500<<20 {
			return nil, errors.New("download too large")
		}
	}
	if progress != nil {
		progress(done, total)
	}
	return buf.Bytes(), nil
}

// findChecksum reads "sha256  filename" lines (sha256sum format).
func findChecksum(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// applyUpdate installs the downloaded asset and returns a function that
// starts the new version.
func applyUpdate(data []byte) (func() error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if goruntime.GOOS == "darwin" {
		return applyMacApp(exe, data)
	}
	if err := replaceExecutable(exe, data); err != nil {
		return nil, err
	}
	return func() error {
		cmd := exec.Command(exe)
		cmd.Dir = filepath.Dir(exe)
		return cmd.Start()
	}, nil
}

// replaceExecutable swaps the running program for a new one. A running .exe
// cannot be overwritten on Windows, but it can be renamed: the old file is
// moved aside to "<name>.old" and removed on the next start.
func replaceExecutable(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".photo2gpx-update-*")
	if err != nil {
		return fmt.Errorf("no write permission in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()
	_ = os.Chmod(tmpName, 0o755)
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(old, exe) // roll back
		os.Remove(tmpName)
		return err
	}
	return nil
}

// applyMacApp replaces the whole Photo2GPX.app bundle with the one in the zip.
func applyMacApp(exe string, data []byte) (func() error, error) {
	// exe = .../Photo2GPX.app/Contents/MacOS/Photo2GPX
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(app, ".app") {
		return nil, errors.New("not running from an .app bundle")
	}
	parent := filepath.Dir(app)
	stage, err := os.MkdirTemp(parent, ".photo2gpx-update-")
	if err != nil {
		return nil, fmt.Errorf("no write permission in %s: %w", parent, err)
	}
	if err := unzip(data, stage); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	var newApp string
	entries, _ := os.ReadDir(stage)
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			newApp = filepath.Join(stage, e.Name())
		}
	}
	if newApp == "" {
		os.RemoveAll(stage)
		return nil, errors.New("no .app in the update archive")
	}
	old := app + ".old"
	os.RemoveAll(old)
	if err := os.Rename(app, old); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	if err := os.Rename(newApp, app); err != nil {
		_ = os.Rename(old, app)
		os.RemoveAll(stage)
		return nil, err
	}
	os.RemoveAll(stage)
	os.RemoveAll(old)
	return func() error { return exec.Command("open", "-n", app).Start() }, nil
}

// unzip extracts an archive, keeping file modes and symlinks (app bundles need both).
func unzip(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	root, _ := filepath.Abs(dst)
	for _, f := range zr.File {
		p := filepath.Join(root, f.Name)
		if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
			return fmt.Errorf("bad path in archive: %s", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			target, _ := io.ReadAll(rc)
			rc.Close()
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.Symlink(string(target), p); err != nil {
				return err
			}
		default:
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			rc, err := f.Open()
			if err != nil {
				return err
			}
			perm := mode.Perm()
			if perm == 0 {
				perm = 0o644
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(out, rc)
			rc.Close()
			out.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupOldUpdate removes the previous version left behind by an update.
func cleanupOldUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return
	}
	go func() {
		// the old process may still be closing; retry for a few seconds
		for i := 0; i < 20; i++ {
			if e := os.Remove(old); e == nil || os.IsNotExist(e) {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

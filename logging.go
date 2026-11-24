package main

// Application log, kept on the user's computer only. Nothing is uploaded:
// "Help → Copy diagnostic info" puts the recent log on the clipboard so the
// user can paste it into a GitHub issue. The log never contains GPS
// coordinates, and the home folder is shown as "~".

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	logFileName = "photo2gpx.log"
	logMaxSize  = 1 << 20 // rotate at 1 MB, keep one previous file
	diagLines   = 200
)

var (
	logger  = slog.New(slog.NewTextHandler(io.Discard, nil))
	logPath string
	logMu   sync.Mutex
	home, _ = os.UserHomeDir()
)

func logDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Photo2GPX", "logs")
}

// initLogging opens the log file (rotating it when it is too big).
func initLogging() {
	dir := logDir()
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	logPath = filepath.Join(dir, logFileName)
	if st, err := os.Stat(logPath); err == nil && st.Size() > logMaxSize {
		_ = os.Rename(logPath, filepath.Join(dir, "photo2gpx.1.log"))
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logPath = ""
		return
	}
	logger = slog.New(slog.NewTextHandler(&lockedWriter{w: f}, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String("time", a.Value.Time().Format("2006-01-02 15:04:05"))
			}
			if a.Value.Kind() == slog.KindString {
				return slog.String(a.Key, redact(a.Value.String()))
			}
			return a
		},
	}))
	logger.Info("---- start ----", "version", AppVersion, "os", goruntime.GOOS, "arch", goruntime.GOARCH, "go", goruntime.Version())
}

type lockedWriter struct{ w io.Writer }

func (l *lockedWriter) Write(p []byte) (int, error) {
	logMu.Lock()
	defer logMu.Unlock()
	return l.w.Write(p)
}

// redact hides the user's home folder (it usually contains their name).
func redact(s string) string {
	if home != "" && len(home) > 3 {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

// LogFrontend records an error reported by the web UI.
func (a *App) LogFrontend(msg string) {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	logger.Error("ui error", "msg", msg)
}

// DiagnosticInfo returns system details plus the end of the log, ready to be
// pasted into a GitHub issue.
func (a *App) DiagnosticInfo() string {
	a.mu.Lock()
	lang, auto := a.lang, a.cfg.AutoUpdate
	np := len(a.photos)
	hasRoute := a.route != nil
	a.mu.Unlock()
	var b strings.Builder
	b.WriteString("```\n")
	fmt.Fprintf(&b, "Photo2GPX %s\n", AppVersion)
	fmt.Fprintf(&b, "OS: %s\n", osDescription())
	fmt.Fprintf(&b, "Language: %s  Auto-update: %v\n", lang, auto)
	fmt.Fprintf(&b, "Photos loaded: %d  Reference route: %v\n", np, hasRoute)
	fmt.Fprintf(&b, "Time: %s\n", time.Now().Format(time.RFC3339))
	b.WriteString("\n--- log (last lines) ---\n")
	for _, l := range tailLines(logPath, diagLines) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	return b.String()
}

// CopyDiagnosticInfo puts the diagnostic text on the clipboard.
func (a *App) CopyDiagnosticInfo() bool {
	if a.ctx == nil {
		return false
	}
	err := runtime.ClipboardSetText(a.ctx, a.DiagnosticInfo())
	if err != nil {
		logger.Error("clipboard", "err", err.Error())
	}
	return err == nil
}

// OpenLogFolder shows the log folder in Explorer / Finder.
func (a *App) OpenLogFolder() {
	dir := logDir()
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		logger.Error("open log folder", "err", err.Error())
	}
}

// ReportProblem opens the "new issue" page of the repository.
func (a *App) ReportProblem() {
	url := "https://github.com/" + GitHubRepo + "/issues/new/choose"
	if !repoConfigured() {
		url = Website
	}
	a.OpenURL(url)
}

func tailLines(path string, n int) []string {
	if path == "" {
		return []string{"(no log file)"}
	}
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return []string{"(log not readable: " + err.Error() + ")"}
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

func osDescription() string {
	s := goruntime.GOOS + "/" + goruntime.GOARCH
	switch goruntime.GOOS {
	case "darwin":
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			s = "macOS " + strings.TrimSpace(string(out)) + " (" + goruntime.GOARCH + ")"
		}
	case "windows":
		if v := windowsVersion(); v != "" {
			s = v + " (" + goruntime.GOARCH + ")"
		}
	}
	return s
}

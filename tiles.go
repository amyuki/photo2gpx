package main

// Map tiles are fetched by the Go side instead of the WebView: the tile
// servers (OpenStreetMap in particular) require an identifying User-Agent and
// reject requests coming from an embedded browser origin. Tiles are cached on
// disk so repeated views do not hit the volunteer-run servers again.

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const userAgent = "Photo2GPX/1.0 (desktop photo-to-GPX tool)"
const tileMaxAge = 14 * 24 * time.Hour

type tileSource struct {
	url  func(z, x, y int) string
	ext  string
	sema chan struct{} // limits parallel connections per server
}

var tileSources = map[string]*tileSource{
	"osm": {
		url: func(z, x, y int) string { return fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", z, x, y) },
		ext: ".png", sema: make(chan struct{}, 2),
	},
	"topo": {
		url: func(z, x, y int) string {
			return fmt.Sprintf("https://%c.tile.opentopomap.org/%d/%d/%d.png", "abc"[rand.Intn(3)], z, x, y)
		},
		ext: ".png", sema: make(chan struct{}, 4),
	},
}

type tileHandler struct {
	client *http.Client
	dir    string
}

func newTileHandler() *tileHandler {
	dir := ""
	if c, err := os.UserCacheDir(); err == nil {
		dir = filepath.Join(c, "Photo2GPX", "tiles")
	}
	return &tileHandler{client: &http.Client{Timeout: 20 * time.Second}, dir: dir}
}

// ServeHTTP handles /tiles/{source}/{z}/{x}/{y}
func (h *tileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "tiles" {
		http.NotFound(w, r)
		return
	}
	src := tileSources[parts[1]]
	z, e1 := strconv.Atoi(parts[2])
	x, e2 := strconv.Atoi(parts[3])
	y, e3 := strconv.Atoi(strings.TrimSuffix(parts[4], ".png"))
	if src == nil || e1 != nil || e2 != nil || e3 != nil || z < 0 || z > 20 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		http.NotFound(w, r)
		return
	}
	var cachePath string
	if h.dir != "" {
		cachePath = filepath.Join(h.dir, parts[1], strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+src.ext)
		if st, err := os.Stat(cachePath); err == nil && time.Since(st.ModTime()) < tileMaxAge {
			if b, err := os.ReadFile(cachePath); err == nil {
				writeTile(w, b)
				return
			}
		}
	}
	src.sema <- struct{}{}
	b, err := h.fetch(src.url(z, x, y))
	<-src.sema
	if err != nil {
		// fall back to a stale cached copy if there is one
		if cachePath != "" {
			if old, e := os.ReadFile(cachePath); e == nil {
				writeTile(w, old)
				return
			}
		}
		logTileError(parts[1], err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if cachePath != "" {
		if os.MkdirAll(filepath.Dir(cachePath), 0o755) == nil {
			tmp := cachePath + ".tmp"
			if os.WriteFile(tmp, b, 0o644) == nil {
				_ = os.Rename(tmp, cachePath)
			}
		}
	}
	writeTile(w, b)
}

func (h *tileHandler) fetch(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tile server returned %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("tile server returned %q", ct)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func writeTile(w http.ResponseWriter, b []byte) {
	ct := http.DetectContentType(b)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(b)
}

var (
	tileErrMu   sync.Mutex
	tileErrLast = map[string]time.Time{}
)

// logTileError logs tile download problems at most once a minute per server.
func logTileError(source string, err error) {
	tileErrMu.Lock()
	defer tileErrMu.Unlock()
	if time.Since(tileErrLast[source]) < time.Minute {
		return
	}
	tileErrLast[source] = time.Now()
	logger.Warn("map tiles unavailable", "source", source, "err", err.Error())
}

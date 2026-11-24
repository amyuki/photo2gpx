package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"photo2gpx/core"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App holds the session state.
type App struct {
	ctx       context.Context
	mu        sync.Mutex
	photos    map[string]*core.Photo
	route     *core.Route
	ov        map[string]core.Override
	dir       string
	last      *core.Result
	thumbs    map[string]string
	errs      []Warning
	lang      string
	menu      *menu.Menu
	menuItems map[string]*menu.MenuItem
	langItems map[string]*menu.MenuItem
	cfg       Config
	updating  bool
	lastList  []*core.Photo
}

func NewApp() *App {
	cfg := loadConfig()
	lang := cfg.Lang
	if _, ok := goStrings[lang]; !ok {
		lang = "en"
	}
	return &App{photos: map[string]*core.Photo{}, ov: map[string]core.Override{}, dir: "auto", thumbs: map[string]string{}, lang: lang, cfg: cfg}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	cleanupOldUpdate()
	if a.cfg.Lang != "" {
		runtime.WindowSetTitle(ctx, goStrings[a.lang]["title"])
	}
	// files passed on the command line (e.g. dropped onto the .exe icon)
	if len(os.Args) > 1 {
		a.AddPaths(os.Args[1:])
	}
}

// ---- view model sent to the frontend ------------------------------------------

type PhotoView struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Local    string      `json:"local"`
	Status   string      `json:"status"`
	Used     bool        `json:"used"`
	HasGPS   bool        `json:"hasGPS"`
	Raw      [2]float64  `json:"raw"`
	Placed   *[2]float64 `json:"placed"`
	OffM     float64     `json:"offM"`
	Override string      `json:"override"`
}

// Warning is a message code with parameters; the UI translates it.
type Warning struct {
	Code string            `json:"code"`
	Args map[string]string `json:"args,omitempty"`
}

type View struct {
	Photos    []PhotoView  `json:"photos"`
	Track     [][2]float64 `json:"track"`
	Route     [][2]float64 `json:"route"`
	RouteName string       `json:"routeName"`
	Reversed  bool         `json:"reversed"`
	Direction string       `json:"direction"`
	DistKm    float64      `json:"distKm"`
	Start     string       `json:"start"`
	End       string       `json:"end"`
	Duration  string       `json:"duration"`
	Ready     bool         `json:"ready"`
	Warnings  []Warning    `json:"warnings"`
}

func ovName(o core.Override) string {
	switch o {
	case core.ForceInclude:
		return "include"
	case core.ForceExclude:
		return "exclude"
	}
	return "auto"
}

// recompute must be called with a.mu held.
func (a *App) recompute() View {
	list := make([]*core.Photo, 0, len(a.photos))
	for _, p := range a.photos {
		list = append(list, p)
	}
	core.FixZones(list)
	r := core.Compute(list, a.route, a.ov, a.dir)
	a.last = r
	a.lastList = list
	v := View{Direction: a.dir, Reversed: r.Reversed, Warnings: append([]Warning{}, a.errs...)}
	for _, c := range r.Warnings {
		v.Warnings = append(v.Warnings, Warning{Code: c})
	}
	if a.route != nil {
		v.Route = core.RouteLatLon(a.route)
		v.RouteName = a.route.Name
	}
	for _, pr := range r.Points {
		p := pr.Photo
		pv := PhotoView{ID: p.ID, Name: p.Name, Local: p.Local, Status: pr.Status, Used: pr.Used,
			HasGPS: p.HasGPS, Raw: [2]float64{p.Lat, p.Lon}, OffM: pr.OffM, Override: ovName(a.ov[p.ID])}
		if pr.HasPos {
			pv.Placed = &[2]float64{pr.Lat, pr.Lon}
		}
		v.Photos = append(v.Photos, pv)
	}
	for _, t := range r.Track {
		v.Track = append(v.Track, [2]float64{t.Lat, t.Lon})
	}
	if len(r.Track) >= 2 {
		v.Ready = true
		off := 0
		for _, pr := range r.Points {
			if pr.Used {
				off = pr.Photo.OffsetSec
				break
			}
		}
		z := time.FixedZone("", off)
		v.Start = r.Start.In(z).Format("2006-01-02 15:04")
		v.End = r.End.In(z).Format("15:04")
		d := r.End.Sub(r.Start).Round(time.Minute)
		v.Duration = fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
		v.DistKm = r.DistM / 1000
	}
	return v
}

// State returns the current view.
func (a *App) State() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.recompute()
}

// AddPaths loads dropped or picked files and folders.
func (a *App) AddPaths(paths []string) View {
	var photos, routes []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			entries, _ := os.ReadDir(p)
			for _, e := range entries {
				fp := filepath.Join(p, e.Name())
				switch {
				case e.IsDir():
				case core.IsPhoto(fp):
					photos = append(photos, fp)
				case core.IsRoute(fp):
					routes = append(routes, fp)
				}
			}
			continue
		}
		switch {
		case core.IsPhoto(p):
			photos = append(photos, p)
		case core.IsRoute(p):
			routes = append(routes, p)
		}
	}
	type loaded struct {
		p    *core.Photo
		err  error
		path string
	}
	out := make([]loaded, len(photos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, p := range photos {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ph, err := core.LoadPhoto(p)
			out[i] = loaded{ph, err, p}
		}(i, p)
	}
	wg.Wait()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.errs = nil
	var skipped []string
	for _, l := range out {
		if l.err != nil {
			skipped = append(skipped, filepath.Base(l.path))
			logger.Warn("photo skipped", "file", filepath.Base(l.path), "err", l.err.Error())
			continue
		}
		a.photos[l.p.ID] = l.p
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		a.errs = append(a.errs, Warning{Code: "skipped", Args: map[string]string{"n": fmt.Sprint(len(skipped)), "names": strings.Join(skipped, ", ")}})
	}
	for _, rp := range routes {
		r, err := core.ReadRoute(rp)
		if err != nil {
			code := "routeFail"
			switch {
			case errors.Is(err, core.ErrRouteEmpty):
				code = "routeEmpty"
			case errors.Is(err, core.ErrRouteFormat):
				code = "routeFormat"
			}
			a.errs = append(a.errs, Warning{Code: code, Args: map[string]string{"name": filepath.Base(rp), "err": err.Error()}})
			logger.Warn("route not loaded", "file", filepath.Base(rp), "err", err.Error())
			continue
		}
		a.route = r
		logger.Info("route loaded", "file", filepath.Base(rp), "points", len(r.Pts))
	}
	if len(photos) == 0 && len(routes) == 0 && len(paths) > 0 {
		a.errs = append(a.errs, Warning{Code: "noFiles"})
	}
	logger.Info("files added", "dropped", len(paths), "photos", len(photos), "skipped", len(skipped), "routes", len(routes), "total_photos", len(a.photos))
	v := a.recompute()
	a.logResult()
	return v
}

// PickPhotos opens a file dialog for photos (and optionally a route).
func (a *App) PickPhotos() View {
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: a.tr("dlgPhotos"),
		Filters: []runtime.FileFilter{
			{DisplayName: a.tr("fltPhotos") + " (*.jpg;*.jpeg;*.gpx;*.kml)", Pattern: "*.jpg;*.jpeg;*.JPG;*.JPEG;*.gpx;*.kml"},
		},
	})
	if err != nil || len(files) == 0 {
		return a.State()
	}
	return a.AddPaths(files)
}

// PickRoute opens a file dialog for the reference route.
func (a *App) PickRoute() View {
	f, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   a.tr("dlgRoute"),
		Filters: []runtime.FileFilter{{DisplayName: "GPX / KML", Pattern: "*.gpx;*.kml"}},
	})
	if err != nil || f == "" {
		return a.State()
	}
	return a.AddPaths([]string{f})
}

// RemoveRoute drops the reference route.
func (a *App) RemoveRoute() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.route = nil
	return a.recompute()
}

// Clear resets everything.
func (a *App) Clear() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.photos = map[string]*core.Photo{}
	a.ov = map[string]core.Override{}
	a.route, a.errs, a.dir = nil, nil, "auto"
	a.thumbs = map[string]string{}
	return a.recompute()
}

// SetOverride sets "auto", "include" or "exclude" for one photo.
func (a *App) SetOverride(id, mode string) View {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.photos[id]; p != nil {
		logger.Info("override", "photo", p.Name, "mode", mode)
	}
	switch mode {
	case "include":
		a.ov[id] = core.ForceInclude
	case "exclude":
		a.ov[id] = core.ForceExclude
	default:
		delete(a.ov, id)
	}
	return a.recompute()
}

// RemovePhoto removes one photo from the session.
func (a *App) RemovePhoto(id string) View {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.photos, id)
	delete(a.ov, id)
	return a.recompute()
}

// SetDirection: "auto", "forward" or "reverse".
func (a *App) SetDirection(d string) View {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dir = d
	v := a.recompute()
	a.logResult()
	return v
}

// Thumbnail returns a data URL preview of the photo.
func (a *App) Thumbnail(id string) string {
	a.mu.Lock()
	p := a.photos[id]
	if t, ok := a.thumbs[id]; ok {
		a.mu.Unlock()
		return t
	}
	a.mu.Unlock()
	if p == nil {
		return ""
	}
	data := makeThumb(p)
	if len(data) == 0 {
		return ""
	}
	url := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
	a.mu.Lock()
	a.thumbs[id] = url
	a.mu.Unlock()
	return url
}

var badChars = regexp.MustCompile(`[\\/:*?"<>|]+`)

// SaveResult is returned to the frontend after saving.
type SaveResult struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Save asks for a file name and writes the GPX.
func (a *App) Save(waypoints bool) SaveResult {
	a.mu.Lock()
	r := a.last
	var name, dir string
	if a.route != nil {
		name = a.route.Name
	}
	for _, p := range a.photos {
		dir = filepath.Dir(p.Path)
		break
	}
	a.mu.Unlock()
	if r == nil || len(r.Track) < 2 {
		return SaveResult{Error: "noTrack"}
	}
	date := r.Start.Format("2006-01-02")
	if name == "" {
		name = a.tr("defaultName")
	}
	def := badChars.ReplaceAllString(date+"_"+name, "_") + ".gpx"
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            a.tr("dlgSave"),
		DefaultDirectory: dir,
		DefaultFilename:  def,
		Filters:          []runtime.FileFilter{{DisplayName: "GPX (*.gpx)", Pattern: "*.gpx"}},
	})
	if err != nil {
		return SaveResult{Error: err.Error()}
	}
	if path == "" {
		return SaveResult{}
	}
	if !strings.HasSuffix(strings.ToLower(path), ".gpx") {
		path += ".gpx"
	}
	if err := os.WriteFile(path, core.WriteGPX(r, name, waypoints), 0o644); err != nil {
		logger.Error("save failed", "path", path, "err", err.Error())
		return SaveResult{Error: err.Error()}
	}
	logger.Info("gpx saved", "path", path, "track_points", len(r.Track), "waypoints", waypoints)
	return SaveResult{Path: path}
}

// ---- language ------------------------------------------------------------------

var goStrings = map[string]map[string]string{
	"zh-Hant": {"title": "相片轉 GPX", "dlgPhotos": "選擇相片", "fltPhotos": "相片與參考路綫", "dlgRoute": "選擇參考路綫", "dlgSave": "保存 GPX", "defaultName": "相片軌跡"},
	"zh-Hans": {"title": "照片转 GPX", "dlgPhotos": "选择照片", "fltPhotos": "照片与参考路线", "dlgRoute": "选择参考路线", "dlgSave": "保存 GPX", "defaultName": "照片轨迹"},
	"en":      {"title": "Photos to GPX", "dlgPhotos": "Choose photos", "fltPhotos": "Photos and reference routes", "dlgRoute": "Choose a reference route", "dlgSave": "Save GPX", "defaultName": "photo track"},
	"fr":      {"title": "Photos vers GPX", "dlgPhotos": "Choisir des photos", "fltPhotos": "Photos et traces de référence", "dlgRoute": "Choisir une trace de référence", "dlgSave": "Enregistrer le GPX", "defaultName": "trace photos"},
}

func (a *App) tr(key string) string {
	a.mu.Lock()
	lang := a.lang
	a.mu.Unlock()
	if m, ok := goStrings[lang]; ok {
		return m[key]
	}
	return goStrings["en"][key]
}

// Prefs is what the UI needs at start-up.
type Prefs struct {
	Lang string `json:"lang"` // "" on first start: the UI detects the system language
}

// GetPrefs returns the stored preferences.
func (a *App) GetPrefs() Prefs {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Prefs{Lang: a.cfg.Lang}
}

// SetLanguage switches the language (UI, menu, dialogs, window title) and
// remembers it. It is a no-op when the language does not change, so the menu
// is not rebuilt needlessly (rebuilding it makes the window flicker).
func (a *App) SetLanguage(lang string) {
	if _, ok := goStrings[lang]; !ok {
		lang = "en"
	}
	a.mu.Lock()
	same := a.lang == lang && a.cfg.Lang == lang
	a.lang = lang
	a.cfg.Lang = lang
	c := a.cfg
	a.mu.Unlock()
	if same {
		return
	}
	logger.Info("language", "lang", lang)
	saveConfig(c)
	if a.ctx != nil {
		runtime.WindowSetTitle(a.ctx, goStrings[lang]["title"])
		if a.menu != nil {
			a.relabelMenu(lang)
			runtime.MenuUpdateApplicationMenu(a.ctx)
		}
		runtime.EventsEmit(a.ctx, "lang", lang)
	}
}

// logResult writes a one-line summary of the last computation (no coordinates).
// Must be called with a.mu held.
func (a *App) logResult() {
	r := a.last
	if r == nil {
		return
	}
	count := map[string]int{}
	for _, p := range r.Points {
		count[p.Status]++
	}
	var warn []string
	for _, w := range r.Warnings {
		warn = append(warn, w)
	}
	cams := map[string]int{}
	for _, p := range a.lastList {
		if p.Model != "" {
			cams[p.Model]++
		}
	}
	logger.Info("track computed",
		"route", r.UsedRoute, "reversed", r.Reversed, "direction", a.dir,
		"km", fmt.Sprintf("%.2f", r.DistM/1000), "track_points", len(r.Track),
		"statuses", fmt.Sprint(count), "cameras", fmt.Sprint(cams), "warnings", strings.Join(warn, ","))
}

package main

import (
	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Set at build time by the release workflow:
//
//	-ldflags "-X main.AppVersion=1.2.3 -X main.GitHubRepo=owner/repo"
var (
	AppVersion = "1.0.0"
	GitHubRepo = "amyuki/photo2gpx" // overridden by the release workflow
)

// App information shown in the About dialog.
const (
	CopyrightYear = "2026"
	Author        = "Yuki Cheung"
	Website       = "https://szeching.com"
	// DonateURL is the project page on the author's website, which links to the
	// donation page; changing where it points does not need a new release.
	DonateURL = "https://szeching.com/photo2gpx-rebuild-gpx-tracks-from-photos/"
)

// Languages offered in the Settings menu, each shown in its own language.
var languages = []struct{ code, label string }{
	{"zh-Hant", "繁體中文"},
	{"zh-Hans", "简体中文"},
	{"en", "English"},
	{"fr", "Français"},
}

// AppInfo is returned to the About dialog.
type AppInfo struct {
	Version string `json:"version"`
	Author  string `json:"author"`
	Website string `json:"website"`
	Donate  string `json:"donate"`
	Year    string `json:"year"`
}

// Info returns the application details.
func (a *App) Info() AppInfo {
	return AppInfo{Version: AppVersion, Author: Author, Website: Website, Donate: DonateURL, Year: CopyrightYear}
}

// OpenURL opens a link in the default browser.
func (a *App) OpenURL(url string) {
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, url)
	}
}

var menuStrings = map[string]map[string]string{
	"zh-Hant": {"file": "檔案", "photos": "選擇相片…", "route": "選擇參考路綫…", "save": "保存 GPX…", "clear": "清除", "exit": "結束",
		"settings": "設定", "language": "語言", "autoUpdate": "啟動時自動檢查更新",
		"help": "說明", "checkUpdate": "檢查更新…", "report": "回報問題…", "copyDiag": "複製診斷資訊", "openLogs": "打開日誌資料夾", "donate": "打賞", "about": "關於相片轉 GPX"},
	"zh-Hans": {"file": "文件", "photos": "选择照片…", "route": "选择参考路线…", "save": "保存 GPX…", "clear": "清除", "exit": "退出",
		"settings": "设置", "language": "语言", "autoUpdate": "启动时自动检查更新",
		"help": "帮助", "checkUpdate": "检查更新…", "report": "反馈问题…", "copyDiag": "复制诊断信息", "openLogs": "打开日志文件夹", "donate": "打赏", "about": "关于照片转 GPX"},
	"en": {"file": "File", "photos": "Choose photos…", "route": "Choose reference route…", "save": "Save GPX…", "clear": "Clear", "exit": "Exit",
		"settings": "Settings", "language": "Language", "autoUpdate": "Check for updates at start-up",
		"help": "Help", "checkUpdate": "Check for updates…", "report": "Report a problem…", "copyDiag": "Copy diagnostic info", "openLogs": "Open log folder", "donate": "Donate", "about": "About Photos to GPX"},
	"fr": {"file": "Fichier", "photos": "Choisir des photos…", "route": "Choisir une trace de référence…", "save": "Enregistrer le GPX…", "clear": "Effacer", "exit": "Quitter",
		"settings": "Réglages", "language": "Langue", "autoUpdate": "Rechercher les mises à jour au démarrage",
		"help": "Aide", "checkUpdate": "Rechercher les mises à jour…", "report": "Signaler un problème…", "copyDiag": "Copier les infos de diagnostic", "openLogs": "Ouvrir le dossier des journaux", "donate": "Faire un don", "about": "À propos de Photos vers GPX"},
}

// buildMenu creates the application menu once. Actions that touch the UI are
// sent to the frontend as a "menu" event so they behave exactly like the buttons.
// Language changes only relabel the existing items (see relabelMenu): replacing
// the whole menu at runtime is not reliable in Wails.
func (a *App) buildMenu(lang string) *menu.Menu {
	emit := func(action string) func(*menu.CallbackData) {
		return func(*menu.CallbackData) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "menu", action)
			}
		}
	}
	a.menuItems = map[string]*menu.MenuItem{}
	a.langItems = map[string]*menu.MenuItem{}
	m := menu.NewMenu()
	if goruntime.GOOS == "darwin" {
		m.Append(menu.AppMenu())
		m.Append(menu.EditMenu())
	}

	file := m.AddSubmenu("file")
	a.menuItems["file"] = m.Items[len(m.Items)-1]
	a.menuItems["photos"] = file.AddText("photos", keys.CmdOrCtrl("o"), emit("photos"))
	a.menuItems["route"] = file.AddText("route", keys.CmdOrCtrl("r"), emit("route"))
	file.AddSeparator()
	a.menuItems["save"] = file.AddText("save", keys.CmdOrCtrl("s"), emit("save"))
	a.menuItems["clear"] = file.AddText("clear", nil, emit("clear"))
	if goruntime.GOOS != "darwin" { // macOS has Quit in the app menu
		file.AddSeparator()
		a.menuItems["exit"] = file.AddText("exit", nil, func(*menu.CallbackData) {
			if a.ctx != nil {
				runtime.Quit(a.ctx)
			}
		})
	}

	settings := m.AddSubmenu("settings")
	a.menuItems["settings"] = m.Items[len(m.Items)-1]
	langMenu := settings.AddSubmenu("language")
	a.menuItems["language"] = settings.Items[len(settings.Items)-1]
	for _, l := range languages {
		code := l.code
		a.langItems[code] = langMenu.AddRadio(l.label, code == lang, nil, func(*menu.CallbackData) {
			go a.SetLanguage(code)
		})
	}
	settings.AddSeparator()
	a.menuItems["autoUpdate"] = settings.AddCheckbox("autoUpdate", a.cfg.AutoUpdate, nil, func(cd *menu.CallbackData) {
		go a.setAutoUpdate(cd.MenuItem.Checked)
	})

	help := m.AddSubmenu("help")
	a.menuItems["help"] = m.Items[len(m.Items)-1]
	a.menuItems["checkUpdate"] = help.AddText("checkUpdate", nil, emit("checkUpdate"))
	help.AddSeparator()
	a.menuItems["report"] = help.AddText("report", nil, func(*menu.CallbackData) { a.ReportProblem() })
	a.menuItems["copyDiag"] = help.AddText("copyDiag", nil, emit("copyDiag"))
	a.menuItems["openLogs"] = help.AddText("openLogs", nil, func(*menu.CallbackData) { a.OpenLogFolder() })
	help.AddSeparator()
	a.menuItems["donate"] = help.AddText("donate", nil, func(*menu.CallbackData) { a.OpenURL(DonateURL) })
	help.AddSeparator()
	a.menuItems["about"] = help.AddText("about", nil, emit("about"))
	a.menu = m
	a.relabelMenu(lang)
	return m
}

// relabelMenu sets the menu labels and the checked language for a language.
func (a *App) relabelMenu(lang string) {
	s, ok := menuStrings[lang]
	if !ok {
		s = menuStrings["en"]
	}
	for k, it := range a.menuItems {
		it.Label = s[k]
	}
	for code, it := range a.langItems {
		it.Checked = code == lang
	}
}

func (a *App) setAutoUpdate(on bool) {
	a.mu.Lock()
	a.cfg.AutoUpdate = on
	c := a.cfg
	a.mu.Unlock()
	saveConfig(c)
}

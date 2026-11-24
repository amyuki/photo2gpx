# Development

## Requirements

- [Go](https://go.dev/dl/), version in `go.mod`.
- Windows builds need nothing else and can be cross-compiled from any system.
- macOS builds need the Xcode command line tools (cgo).
- Running the desktop app or the full test suite on Linux needs WebKitGTK: `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev`, and the build tag `webkit2_41`.

The frontend is plain HTML / CSS / JavaScript in `frontend/dist` (no build step, no Node.js).

## Project layout

| Path | Contents |
|---|---|
| `main.go` | Wails application setup |
| `app.go` | State, file loading, track computation, save dialog; bound to the UI |
| `core/` | EXIF reader, GPX/KML reader, route matching (HMM / Viterbi), GPX writer — no UI code |
| `menu.go` | Menus, version, author, donation link (`DonateURL`) |
| `config.go` | User settings (`config.json`) |
| `update.go` | Update check and self-update from GitHub Releases |
| `logging.go` | Local application log and the "Copy diagnostic info" text |
| `tiles.go` | Map tiles downloaded and cached by the Go side |
| `frontend/dist/` | UI: `index.html`, `app.js`, `style.css`, `i18n.js` (translations), Leaflet |
| `build/` | Icons and macOS `Info.plist` template |
| `devserver.go` | Browser preview of the UI (build tag `dev`) |

## Run

```
# browser preview of the UI, backed by the real Go code (no native dialogs or menus)
go run -tags dev .
# then open http://127.0.0.1:34115

# desktop app on Linux
go run -tags desktop,production,webkit2_41 .
```

## Build

```
# Windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -tags desktop,production -ldflags "-H windowsgui -s -w" -o build/Photo2GPX.exe .

# macOS
go build -trimpath -tags desktop,production -o build/Photo2GPX .
```

Local builds use the version and repository set in `menu.go`. Release builds override them with `-ldflags "-X main.AppVersion=… -X main.GitHubRepo=…"` (see [RELEASING.md](RELEASING.md)).

`rsrc_windows_amd64.syso` holds the Windows icon and manifest so that a plain `go build` gets them. Regenerate it after changing the icon:

```
go run github.com/tc-hib/go-winres@v0.3.3 simply --icon build/icon.png --manifest gui --out rsrc
rm rsrc_windows_386.syso
```

## Test

```
go test ./core                       # algorithms, any system
go test -tags webkit2_41 ./...       # everything, on Linux
```

`core/testdata/` is not in the repository (it contains personal GPS data). Tests that need it are skipped when it is missing.

## Logging

The app writes a log to `<config dir>/Photo2GPX/logs/photo2gpx.log` (rotated at 1 MB). Use `logger.Info/Warn/Error` from `logging.go`. Never log GPS coordinates; paths are written with the home folder replaced by `~`.

## Translations

UI strings are in `frontend/dist/i18n.js`. Menu, window title and native dialog strings are in `menuStrings` (`menu.go`) and `goStrings` (`app.go`). All four languages must have the same keys.

# Photo2GPX

[![Latest release](https://img.shields.io/github/v/release/amyuki/photo2gpx)](../../releases/latest)
[![Release build](https://github.com/amyuki/photo2gpx/actions/workflows/release.yml/badge.svg)](../../actions/workflows/release.yml)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

Rebuild a GPX track from your photos: reads GPS and capture time, filters bad positions, snaps to a reference route, and lets you check it on a map before saving. Windows & macOS.

![Photo2GPX: drop photos, check the track on the map, save as GPX](docs/demo.gif)

## What is it for

You came back from a walk, a hike or a trip with photos but no GPS recording. Your phone photos still know where and when they were taken. Photo2GPX reads that information and turns it into a GPX track you can import into map apps, Strava, gpx.studio and similar tools.

- Drop photos (or a whole folder) into the window.
- Optionally add the route you followed (`.gpx` or `.kml`); the photo positions are snapped onto it.
- Check everything on the map, fix wrong points with a click, and save.

## Features

- **Filters bad positions**: phones sometimes reuse an old cached position, or report a jump of several hundred metres. These points are detected and ignored, and shown on the map so you can decide.
- **Snaps to a reference route**, with automatic detection of the walking direction. Turning back along the route is supported.
- **Camera photos without GPS** are placed on the track by capture time, so they can be added as photo waypoints.
- **Time zones** are read from the photos, so phone and camera photos line up.
- **Map preview** on OpenTopoMap or OpenStreetMap, with a thumbnail of each photo.
- **Checks for updates** and updates itself.
- **Languages**: English, Français, 繁體中文, 简体中文.

## Supported platforms

- **Windows 10 / 11**, 64-bit. Uses Microsoft Edge WebView2, which is included in Windows 10/11.
- **macOS 12 (Monterey) or later**, Apple Silicon and Intel.

## Installation

Download the latest version from [Releases](../../releases/latest).

**Windows**: download `Photo2GPX-windows-amd64.exe` and double-click it. There is nothing to install. You can keep it anywhere, for example on the Desktop or in Documents.

**macOS**: download `Photo2GPX-macos-universal.dmg`, open it and drag Photo2GPX into Applications.
The app is not notarized by Apple, so macOS blocks it the first time. Open **System Settings → Privacy & Security**, scroll down to Photo2GPX and click **Open Anyway**. You only need to do this once.

## How to use

1. Drop photos into the window. You can also drop a whole folder, or on Windows drop photos onto the exe icon.
2. Optional: drop a reference route (`.gpx` or `.kml`) at the same time, for example a track downloaded from a route website. The photo positions are then snapped onto that route.
3. Check the result on the map. Click any point to see the photo, and to exclude it, use it anyway, or remove it. **To check** in the list shows only the points that need a look.
4. Click **Confirm and save GPX**.

Ignored points are shown as hollow circles at the position the phone recorded. If one is actually right, click it and choose **Use anyway**.

## Settings and updates

- **Settings → Language** switches the language. The first time, the app follows your system language.
- **Settings → Check for updates at start-up** can be turned off. **Help → Check for updates** checks at any time.
- When an update is available, the app shows what's new. **Update now** downloads it, checks that the file is intact, replaces the program and restarts.
- Settings are stored in `%AppData%\Photo2GPX` on Windows and `~/Library/Application Support/Photo2GPX` on macOS.

The app connects to the internet only to download map tiles from OpenTopoMap / OpenStreetMap (cached for 14 days; like any online map, the tile servers see which area is displayed) and to check for updates on GitHub. Your photos, their positions and your tracks are never uploaded.

## Troubleshooting and feature requests

Found a bug or have an idea? [Open an issue](../../issues/new/choose).

When reporting a problem, please include the diagnostic info: choose **Help → Copy diagnostic info** in the app and paste it into the issue. It contains the app version, your system version and the recent application log. The log does not contain GPS coordinates, and your home folder is shown as `~`. Nothing is sent automatically.

The log files are in the `logs` folder next to the settings (**Help → Open log folder**).

## Languages and translations

| Language | Status |
|---|---|
| English | complete |
| Français | complete |
| 繁體中文 | complete |
| 简体中文 | complete |

To add or improve a translation, edit [`frontend/dist/i18n.js`](frontend/dist/i18n.js) (the interface) and the `menuStrings` and `goStrings` tables in [`menu.go`](menu.go) and [`app.go`](app.go) (menus, window title and file dialogs), then open a pull request. Copy the `en` block as a starting point.

## Build from source

You need [Go](https://go.dev/dl/) (version in `go.mod`). No Node.js is needed.

```
# Windows exe (also works from Linux or macOS)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags desktop,production -ldflags "-H windowsgui" -o Photo2GPX.exe .

# macOS (on a Mac with Xcode command line tools)
go build -tags desktop,production -o Photo2GPX .
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the project layout, tests and the browser preview.

## Support

Photo2GPX is free. If it is useful to you, you can support its development with a donation from **Help → Donate** in the app.

## License

Photo2GPX is free software, released under the [GNU General Public License v3.0](LICENSE). You may use, study, share and modify it; if you distribute a modified version, it must also be released under the GPL-3.0 with its source code.

## Credits

- Map data © [OpenStreetMap](https://www.openstreetmap.org/copyright) contributors, map style © [OpenTopoMap](https://opentopomap.org) (CC-BY-SA).
- Built with [Wails](https://wails.io) (MIT) and [Leaflet](https://leafletjs.com) (BSD-2-Clause, see `frontend/dist/leaflet/LICENSE`).

© 2026 Yuki Cheung · [szeching.com](https://szeching.com)

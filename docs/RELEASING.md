# Releasing

Releases are built and published by GitHub Actions ([`.github/workflows/release.yml`](../.github/workflows/release.yml)) when a version tag is pushed.

## Publish a release

```
git tag v1.1.0
git push origin v1.1.0
```

The workflow runs the tests, builds Windows and macOS, and publishes the release. Follow it in the **Actions** tab.

- The version comes from the tag (`v1.1.0` → `1.1.0`) and is written into the app, together with the repository name. No code change is needed for a release.
- Tags containing `-` (for example `v1.2.0-beta.1`) are published as **pre-releases**. The in-app updater ignores them.
- Release notes are generated from merged pull requests and commits. Edit them on the release page if needed: the text is shown in the app's update dialog, so write it for users.
- To test the build without publishing, run the **Release** workflow by hand from the Actions tab (**Run workflow**). The files are attached to the run as artifacts.

## Release files

| File | Purpose |
|---|---|
| `Photo2GPX-windows-amd64.exe` | Windows program; also downloaded by the updater |
| `Photo2GPX-macos-universal.dmg` | macOS installer for people |
| `Photo2GPX-macos-universal.zip` | macOS app bundle; downloaded by the updater |
| `checksums.txt` | SHA-256 of every file; the updater refuses a file that does not match |
| Source code (zip / tar.gz) | added automatically by GitHub |

**Do not rename these files**: the updater in already-installed copies looks for these exact names.

## How the updater works

1. Reads `https://api.github.com/repos/<owner>/<repo>/releases/latest` (no login, so the repository must stay **public**).
2. Compares the tag with the running version.
3. Downloads the file for the system and `checksums.txt`, and checks the SHA-256.
4. Windows: renames the running exe to `Photo2GPX.exe.old`, writes the new one, starts it; the `.old` file is deleted on the next start.
   macOS: unpacks the zip next to the installed app, swaps the whole `Photo2GPX.app`, reopens it.
5. If the program's folder is not writable, it opens the release page instead.

Users can skip a version or turn off the start-up check in Settings.

## Notes

- The macOS app is ad-hoc signed but not notarized: users need **System Settings → Privacy & Security → Open Anyway** the first time. Notarization needs a paid Apple Developer account and changes to the workflow (signing identity, `notarytool`).
- The Windows exe is not code-signed. SmartScreen may warn on first download from a browser.
- The Windows icon, manifest and version information are generated from `build/icon.png` during the build (go-winres). `build/appicon.png` (1024 px) is used for the macOS icon.

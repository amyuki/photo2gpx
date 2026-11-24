package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.0.1", "1.0.0", true}, {"v1.0.0", "1.0.0", false}, {"1.2", "1.10.0", false},
		{"v2.0.0", "1.99.99", true}, {"v1.10.0", "1.9.9", true}, {"v1.0.0-beta", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	sums := []byte("abc123  Photo2GPX-windows-amd64.exe\nDEF456 *Photo2GPX-macos-universal.zip\n")
	if findChecksum(sums, "Photo2GPX-windows-amd64.exe") != "abc123" || findChecksum(sums, "Photo2GPX-macos-universal.zip") != "def456" || findChecksum(sums, "x") != "" {
		t.Fatal("checksum parsing")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Photo2GPX.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := replaceExecutable(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe content %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("old content %q", b)
	}
	// a second update must work even though .old exists
	if err := replaceExecutable(exe, []byte("newer")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "newer" {
		t.Fatal("second update")
	}
}

func TestUnzipKeepsModesAndSymlinks(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, mode os.FileMode, body string) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(body))
	}
	add("Photo2GPX.app/", os.ModeDir|0o755, "")
	add("Photo2GPX.app/Contents/MacOS/Photo2GPX", 0o755, "bin")
	add("Photo2GPX.app/Contents/Info.plist", 0o644, "plist")
	add("Photo2GPX.app/Contents/Frameworks/Current", os.ModeSymlink|0o777, "A")
	zw.Close()
	dst := t.TempDir()
	if err := unzip(buf.Bytes(), dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dst, "Photo2GPX.app/Contents/MacOS/Photo2GPX"))
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary not executable: %v %v", st, err)
	}
	if l, err := os.Readlink(filepath.Join(dst, "Photo2GPX.app/Contents/Frameworks/Current")); err != nil || l != "A" {
		t.Fatalf("symlink %q %v", l, err)
	}
	// zip-slip must be refused
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.Create("../evil")
	w.Write([]byte("x"))
	zw.Close()
	if err := unzip(buf.Bytes(), t.TempDir()); err == nil {
		t.Fatal("zip-slip accepted")
	}
}

func TestCheckAndDownloadFromFakeGitHub(t *testing.T) {
	payload := []byte("new program bytes")
	sum := sha256.Sum256(payload)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/me/photo2gpx/releases/latest":
			json.NewEncoder(w).Encode(ghRelease{Tag: "v9.9.9", Body: "notes", HTMLURL: "https://example/rel", Assets: []ghAsset{
				{Name: "Photo2GPX-windows-amd64.exe", URL: srv.URL + "/dl/exe"},
				{Name: "Photo2GPX-macos-universal.zip", URL: srv.URL + "/dl/zip"},
				{Name: "checksums.txt", URL: srv.URL + "/dl/sums"},
			}})
		case "/dl/exe":
			w.Write(payload)
		case "/dl/sums":
			fmt.Fprintf(w, "%s  Photo2GPX-windows-amd64.exe\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldAPI, oldRepo := githubAPI, GitHubRepo
	githubAPI, GitHubRepo = srv.URL, "me/photo2gpx"
	defer func() { githubAPI, GitHubRepo = oldAPI, oldRepo }()

	a := NewApp()
	info := a.CheckUpdate()
	if !info.Available || info.Latest != "9.9.9" || info.Error != "" {
		t.Fatalf("check: %+v", info)
	}
	a.cfg.AutoUpdate, a.cfg.SkipVersion = true, "9.9.9"
	if a.AutoCheckUpdate().Available {
		t.Fatal("skipped version offered again")
	}
	sums, _ := download(srv.URL+"/dl/sums", nil)
	got, err := download(srv.URL+"/dl/exe", func(int64, int64) {})
	h := sha256.Sum256(got)
	if err != nil || findChecksum(sums, "Photo2GPX-windows-amd64.exe") != hex.EncodeToString(h[:]) {
		t.Fatal("download/verify failed")
	}
	// placeholder repo: no network call, no error shown at start-up
	GitHubRepo = "OWNER/photo2gpx"
	if a.AutoCheckUpdate().Available {
		t.Fatal("placeholder repo should not report updates")
	}
}

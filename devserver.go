//go:build dev

// Browser preview of the UI for development: go run -tags dev . [addr]
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"photo2gpx/core"
	"reflect"
	"strings"
	"time"
)

const shim = `<script>
window.go={main:{App:new Proxy({},{get:(_,name)=>(...args)=>fetch('/rpc/'+name,{method:'POST',body:JSON.stringify(args)}).then(r=>r.json())})}};
window.runtime={OnFileDrop:function(){},EventsOn:function(){},BrowserOpenURL:function(u){console.log('open',u)}};
</script>`

func main() {
	initLogging()
	app := NewApp()
	addr := "127.0.0.1:34115"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	if dir := os.Getenv("DEV_FIXTURE"); dir != "" {
		devLoadFixture(app, dir)
	}
	// /dev/load?dir=…: load the test fixture on demand (used to record demos)
	http.HandleFunc("/dev/load", func(w http.ResponseWriter, r *http.Request) {
		devLoadFixture(app, r.URL.Query().Get("dir"))
		w.Write([]byte("ok"))
	})
	http.Handle("/tiles/", newTileHandler())
	fs := http.FileServer(http.Dir("frontend/dist"))
	http.HandleFunc("/rpc/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/rpc/")
		m := reflect.ValueOf(app).MethodByName(name)
		if !m.IsValid() {
			http.Error(w, "no method", 404)
			return
		}
		var raw []json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		in := make([]reflect.Value, m.Type().NumIn())
		for i := range in {
			v := reflect.New(m.Type().In(i))
			if i < len(raw) {
				_ = json.Unmarshal(raw[i], v.Interface())
			}
			in[i] = v.Elem()
		}
		out := m.Call(in)
		var res interface{}
		if len(out) > 0 {
			res = out[0].Interface()
		}
		_ = json.NewEncoder(w).Encode(res)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, _ := os.ReadFile("frontend/dist/index.html")
			s := strings.Replace(string(b), "<script src=\"leaflet/leaflet.js\">", shim+"<script src=\"leaflet/leaflet.js\">", 1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(s))
			return
		}
		fs.ServeHTTP(w, r)
	})
	log.Println("dev UI on http://" + addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// devLoadFixture loads core/testdata (photo metadata + route) and any real JPEGs in dir.
func devLoadFixture(a *App, dir string) {
	b, err := os.ReadFile("core/testdata/photos_2026-04-04.json")
	if err != nil {
		log.Fatal(err)
	}
	var fx []struct {
		Name, Local, Offset string
		Lat, Lon, Alt       *float64
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		log.Fatal(err)
	}
	real := map[string]bool{}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			real[e.Name()] = true
		}
	}
	for _, f := range fx {
		if real[f.Name] {
			continue
		}
		local, _ := core.ParseLocal(f.Local)
		off, _ := core.ParseOffset(f.Offset)
		p := &core.Photo{ID: f.Name, Name: f.Name, Path: f.Name, HasTime: true, OffsetSec: off,
			Time: local.Add(-time.Duration(off) * time.Second), Local: local.Format("15:04:05")}
		if f.Lat != nil {
			p.HasGPS, p.Lat, p.Lon = true, *f.Lat, *f.Lon
		}
		a.photos[p.ID] = p
	}
	route := os.Getenv("DEV_ROUTE")
	if route == "" {
		route = "core/testdata/randogps_74_75.gpx"
	}
	paths := []string{route}
	for n := range real {
		paths = append(paths, filepath.Join(dir, n))
	}
	a.AddPaths(paths)
}

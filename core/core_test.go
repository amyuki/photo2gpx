package core

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fixturePhoto struct {
	Name   string   `json:"name"`
	Local  string   `json:"local"`
	Offset string   `json:"offset"`
	Lat    *float64 `json:"lat"`
	Lon    *float64 `json:"lon"`
	Alt    *float64 `json:"alt"`
}

func loadFixture(t *testing.T) []*Photo {
	b, err := os.ReadFile("testdata/photos_2026-04-04.json")
	if os.IsNotExist(err) {
		t.Skip("private test fixture not present (core/testdata is not published)")
	}
	if err != nil {
		t.Fatal(err)
	}
	var fx []fixturePhoto
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	var out []*Photo
	for _, f := range fx {
		local, err := ParseLocal(f.Local)
		if err != nil {
			t.Fatal(err)
		}
		off, _ := ParseOffset(f.Offset)
		p := &Photo{ID: f.Name, Name: f.Name, HasTime: true, OffsetSec: off,
			Time: local.Add(-time.Duration(off) * time.Second), Local: local.Format("15:04:05")}
		if f.Lat != nil {
			p.HasGPS, p.Lat, p.Lon = true, *f.Lat, *f.Lon
			if f.Alt != nil {
				p.Alt, p.HasAlt = *f.Alt, true
			}
		}
		out = append(out, p)
	}
	return out
}

func TestExifRealPhotos(t *testing.T) {
	dir := os.Getenv("PHOTO_DIR")
	if dir == "" {
		t.Skip("PHOTO_DIR not set")
	}
	cases := []struct {
		name     string
		gps      bool
		lat, lon float64
		local    string
	}{
		{"PXL_20260404_145407806.jpg", true, 46.3181083, 6.60375, "16:54:07"},
		{"PXL_20260404_125134134.MP.jpg", true, 46.3212167, 6.6163694, "14:51:34"},
		{"DSC05196.jpg", false, 0, 0, "12:31:08"},
	}
	for _, c := range cases {
		p, err := LoadPhoto(filepath.Join(dir, c.name))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.HasGPS != c.gps || (c.gps && (math.Abs(p.Lat-c.lat) > 1e-6 || math.Abs(p.Lon-c.lon) > 1e-6)) {
			t.Errorf("%s: gps %v %f %f", c.name, p.HasGPS, p.Lat, p.Lon)
		}
		if p.Local != c.local || p.OffsetSec != 7200 {
			t.Errorf("%s: local %s offset %d", c.name, p.Local, p.OffsetSec)
		}
		t.Logf("%s ok: utc=%s thumb=%d bytes model alt=%.1f", c.name, p.Time.Format(time.RFC3339), len(p.Thumb), p.Alt)
	}
}

func TestFitWithRoute(t *testing.T) {
	ph := loadFixture(t)
	rt, err := ReadRoute("testdata/randogps_74_75.gpx")
	if os.IsNotExist(err) {
		t.Skip("route fixture not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r := Compute(ph, rt, nil, "auto")
	t.Logf("compute took %v", time.Since(start))
	count := map[string]int{}
	for _, p := range r.Points {
		count[p.Status]++
	}
	t.Logf("reversed=%v dist=%.0f m start=%s end=%s statuses=%v track=%d",
		r.Reversed, r.DistM, r.Start.Add(2*time.Hour).Format("15:04"), r.End.Add(2*time.Hour).Format("15:04"), count, len(r.Track))
	if !r.Reversed {
		t.Error("expected the route to be followed against the file order")
	}
	if r.DistM < 4800 || r.DistM > 5800 {
		t.Errorf("unexpected distance %.0f", r.DistM)
	}
	if count[StCached] < 15 {
		t.Errorf("expected cached fixes to be detected, got %d", count[StCached])
	}
	for _, p := range r.Points {
		if p.Photo.Name == "PXL_20260404_103631313.jpg" && p.Used {
			t.Error("the 1.4 km jump at 12:36 should not be used")
		}
		if p.Status == StNoGPS && !p.HasPos {
			t.Errorf("camera photo %s not placed", p.Photo.Name)
		}
	}
	// user includes the two last points (back at the road): track must backtrack
	ov := map[string]Override{"PXL_20260404_153958378.jpg": ForceInclude, "PXL_20260404_154001824.jpg": ForceInclude}
	r2 := Compute(ph, rt, ov, "auto")
	last := r2.Track[len(r2.Track)-1]
	d := HaversineM(last.Lat, last.Lon, 46.3180139, 6.6055194)
	t.Logf("with override: dist=%.0f m end=%s last point %.0f m from 17:39 fix", r2.DistM, r2.End.Add(2*time.Hour).Format("15:04"), d)
	if d > 60 {
		t.Errorf("track should end near the 17:39 position, is %.0f m away", d)
	}
	gpx := WriteGPX(r2, "test", true)
	if err := os.WriteFile(filepath.Join(os.TempDir(), "photo2gpx_test.gpx"), gpx, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFitNoRoute(t *testing.T) {
	ph := loadFixture(t)
	r := Compute(ph, nil, nil, "auto")
	count := map[string]int{}
	for _, p := range r.Points {
		count[p.Status]++
	}
	t.Logf("no route: dist=%.0f m statuses=%v", r.DistM, count)
	for _, p := range r.Points {
		if p.Photo.Name == "PXL_20260404_103631313.jpg" && p.Used {
			t.Error("the 1.4 km jump at 12:36 should be dropped")
		}
		if p.Photo.Name == "PXL_20260404_120947305.jpg" && p.Used {
			t.Error("the 14:09 jump should be dropped")
		}
	}
	if r.DistM > 7000 {
		t.Errorf("no-route distance too large: %.0f", r.DistM)
	}
}

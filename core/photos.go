package core

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// IsPhoto reports whether the path looks like a supported photo.
func IsPhoto(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return true
	}
	return false
}

// IsRoute reports whether the path looks like a reference route.
func IsRoute(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gpx", ".kml":
		return true
	}
	return false
}

// LoadPhoto reads one photo. The UTC time may still need a zone (see FixZones).
func LoadPhoto(path string) (*Photo, error) {
	ex, err := ReadExif(path)
	if err != nil {
		return nil, err
	}
	p := &Photo{ID: path, Path: path, Name: filepath.Base(path), Thumb: ex.Thumb, Model: ex.Model, Orientation: ex.Orientation}
	if ex.HasGPS {
		p.HasGPS, p.Lat, p.Lon = true, ex.Lat, ex.Lon
		p.Alt, p.HasAlt = ex.Alt, ex.HasAlt
	}
	if local, err := ParseLocal(ex.LocalTime); err == nil {
		p.HasTime = true
		p.Local = local.Format("15:04:05")
		if off, ok := ParseOffset(ex.Offset); ok {
			p.OffsetSec = off
			p.Time = local.Add(-time.Duration(off) * time.Second)
		} else if ex.HasGPSTime {
			// derive the zone from the GPS clock, rounded to 15 minutes
			d := local.Sub(ex.GPSTimeUTC).Seconds()
			off := int(math.Round(d/900) * 900)
			p.OffsetSec = off
			p.Time = local.Add(-time.Duration(off) * time.Second)
		} else {
			p.OffsetSec = math.MinInt32 // unknown, resolved by FixZones
			p.Time = local
		}
	} else if ex.HasGPSTime {
		p.HasTime = true
		p.Time = ex.GPSTimeUTC
		p.Local = ex.GPSTimeUTC.Format("15:04:05") + " UTC"
	}
	return p, nil
}

// FixZones gives photos without any zone information the most common zone of
// the other photos (or the computer's zone), so camera and phone align.
func FixZones(ph []*Photo) {
	count := map[int]int{}
	for _, p := range ph {
		if p.HasTime && p.OffsetSec != math.MinInt32 {
			count[p.OffsetSec]++
		}
	}
	best, bc := 0, -1
	for off, c := range count {
		if c > bc {
			best, bc = off, c
		}
	}
	for _, p := range ph {
		if !p.HasTime || p.OffsetSec != math.MinInt32 {
			continue
		}
		local := p.Time // stored as naive local
		off := best
		if bc < 0 {
			_, off = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.Local).Zone()
		}
		p.OffsetSec = off
		p.Time = local.Add(-time.Duration(off) * time.Second)
	}
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var statusText = map[string]string{
	StOK:       "on track",
	StSnapped:  "snapped onto the route",
	StCached:   "cached GPS fix ignored, placed by time",
	StOutlier:  "wrong GPS fix ignored, placed by time",
	StExcluded: "GPS excluded by user, placed by time",
	StNoGPS:    "no GPS, placed by time",
}

// WriteGPX renders the result as a GPX 1.1 document.
func WriteGPX(r *Result, name string, waypoints bool) []byte {
	iso := func(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<gpx version="1.1" creator="Photo2GPX" xmlns="http://www.topografix.com/GPX/1/1">` + "\n")
	fmt.Fprintf(&b, "<metadata><name>%s</name><desc>Generated from photo GPS, %.2f km</desc><time>%s</time></metadata>\n",
		xmlEsc(name), r.DistM/1000, iso(r.Start))
	if waypoints {
		for _, pr := range r.Points {
			if !pr.HasPos {
				continue
			}
			fmt.Fprintf(&b, `<wpt lat="%.7f" lon="%.7f">`, pr.Lat, pr.Lon)
			if pr.HasEle {
				fmt.Fprintf(&b, "<ele>%.1f</ele>", pr.Ele)
			}
			fmt.Fprintf(&b, "<time>%s</time><name>%s</name><desc>%s</desc><sym>Photo</sym></wpt>\n",
				iso(pr.Photo.Time), xmlEsc(pr.Photo.Name), xmlEsc(statusText[pr.Status]))
		}
	}
	fmt.Fprintf(&b, "<trk><name>%s</name><type>hiking</type><trkseg>\n", xmlEsc(name))
	for _, t := range r.Track {
		fmt.Fprintf(&b, `<trkpt lat="%.7f" lon="%.7f">`, t.Lat, t.Lon)
		if t.HasEle {
			fmt.Fprintf(&b, "<ele>%.1f</ele>", t.Ele)
		}
		fmt.Fprintf(&b, "<time>%s</time></trkpt>\n", iso(t.Time))
	}
	b.WriteString("</trkseg></trk>\n</gpx>\n")
	return b.Bytes()
}

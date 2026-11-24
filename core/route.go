package core

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RoutePt is a point of the reference route.
type RoutePt struct {
	Lat, Lon, Ele float64
	HasEle        bool
}

// Route is a reference route read from a GPX or KML file.
type Route struct {
	Name string
	Pts  []RoutePt
}

// Errors returned by ReadRoute.
var (
	ErrRouteFormat = errors.New("only .gpx and .kml routes are supported")
	ErrRouteEmpty  = errors.New("no track points found in the route file")
)

// ReadRoute reads a reference route from .gpx or .kml.
func ReadRoute(path string) (*Route, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r *Route
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gpx":
		r, err = readGPX(f)
	case ".kml":
		r, err = readKML(f)
	default:
		return nil, ErrRouteFormat
	}
	if err != nil {
		return nil, err
	}
	if len(r.Pts) < 2 {
		return nil, ErrRouteEmpty
	}
	if r.Name == "" || strings.EqualFold(r.Name, "track") {
		r.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return r, nil
}

func attrF(se xml.StartElement, name string) (float64, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			v, err := strconv.ParseFloat(strings.TrimSpace(a.Value), 64)
			return v, err == nil
		}
	}
	return 0, false
}

func readGPX(rd io.Reader) (*Route, error) {
	d := xml.NewDecoder(rd)
	var trk, rte []RoutePt
	var cur *RoutePt
	var curKind string
	var stack []string
	var trkName, rteName, metaName string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if t.Name.Local == "trkpt" || t.Name.Local == "rtept" {
				lat, ok1 := attrF(t, "lat")
				lon, ok2 := attrF(t, "lon")
				if ok1 && ok2 {
					cur = &RoutePt{Lat: lat, Lon: lon}
					curKind = t.Name.Local
				}
			}
		case xml.CharData:
			if len(stack) < 2 {
				continue
			}
			el, parent := stack[len(stack)-1], stack[len(stack)-2]
			s := strings.TrimSpace(string(t))
			if el == "ele" && cur != nil {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					cur.Ele, cur.HasEle = v, true
				}
			}
			if el == "name" {
				switch parent {
				case "trk":
					if trkName == "" {
						trkName = s
					}
				case "rte":
					if rteName == "" {
						rteName = s
					}
				case "metadata":
					metaName = s
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if cur != nil && t.Name.Local == curKind {
				if curKind == "trkpt" {
					trk = append(trk, *cur)
				} else {
					rte = append(rte, *cur)
				}
				cur = nil
			}
		}
	}
	r := &Route{}
	if len(trk) >= 2 {
		r.Pts, r.Name = trk, trkName
	} else {
		r.Pts, r.Name = rte, rteName
	}
	if r.Name == "" {
		r.Name = metaName
	}
	return r, nil
}

func readKML(rd io.Reader) (*Route, error) {
	d := xml.NewDecoder(rd)
	var best []RoutePt
	var gx []RoutePt
	var name string
	var stack []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			el := stack[len(stack)-1]
			switch el {
			case "name":
				if name == "" && len(stack) >= 2 && (stack[len(stack)-2] == "Document" || stack[len(stack)-2] == "Placemark") {
					name = strings.TrimSpace(string(t))
				}
			case "coordinates":
				var pts []RoutePt
				for _, tup := range strings.Fields(string(t)) {
					p := strings.Split(tup, ",")
					if len(p) < 2 {
						continue
					}
					lon, e1 := strconv.ParseFloat(p[0], 64)
					lat, e2 := strconv.ParseFloat(p[1], 64)
					if e1 != nil || e2 != nil {
						continue
					}
					rp := RoutePt{Lat: lat, Lon: lon}
					if len(p) >= 3 {
						if v, err := strconv.ParseFloat(p[2], 64); err == nil && v != 0 {
							rp.Ele, rp.HasEle = v, true
						}
					}
					pts = append(pts, rp)
				}
				if len(pts) > len(best) {
					best = pts
				}
			case "coord": // gx:Track
				p := strings.Fields(string(t))
				if len(p) >= 2 {
					lon, e1 := strconv.ParseFloat(p[0], 64)
					lat, e2 := strconv.ParseFloat(p[1], 64)
					if e1 == nil && e2 == nil {
						rp := RoutePt{Lat: lat, Lon: lon}
						if len(p) >= 3 {
							if v, err := strconv.ParseFloat(p[2], 64); err == nil {
								rp.Ele, rp.HasEle = v, true
							}
						}
						gx = append(gx, rp)
					}
				}
			}
		}
	}
	if len(gx) > len(best) {
		best = gx
	}
	return &Route{Name: name, Pts: best}, nil
}

// ---- local planar projection -------------------------------------------------

type proj struct{ kx, ky float64 }

func newProj(lat0 float64) proj {
	const R = 6371000.0
	k := R * math.Pi / 180
	return proj{kx: k * math.Cos(lat0*math.Pi/180), ky: k}
}

func (p proj) xy(lat, lon float64) (float64, float64) { return lon * p.kx, lat * p.ky }

// HaversineM returns the great-circle distance in metres.
func HaversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	r := math.Pi / 180
	dlat, dlon := (lat2-lat1)*r, (lon2-lon1)*r
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*r)*math.Cos(lat2*r)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * R * math.Asin(math.Min(1, math.Sqrt(a)))
}

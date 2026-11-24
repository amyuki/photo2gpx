package core

import (
	"math"
	"sort"
	"time"
)

// Photo is one dropped image.
type Photo struct {
	ID, Path, Name string
	HasTime        bool
	Time           time.Time // UTC
	Local          string    // local clock time as shown by the camera, "15:04:05"
	OffsetSec      int
	HasGPS         bool
	Lat, Lon, Alt  float64
	HasAlt         bool
	Thumb          []byte
	Model          string // camera / phone model from EXIF
	Orientation    int    // EXIF orientation 1-8
}

// Override is a user decision on one photo's GPS point.
type Override int

const (
	Auto Override = iota
	ForceInclude
	ForceExclude
)

// Status values reported per photo.
const (
	StOK       = "ok"       // used, close to the route (or used in no-route mode)
	StSnapped  = "snapped"  // used, 60-200 m from route, snapped onto it
	StCached   = "cached"   // auto-excluded: repeated cached GPS fix
	StOutlier  = "outlier"  // auto-excluded: far from route / impossible jump
	StExcluded = "excluded" // excluded by the user
	StNoGPS    = "nogps"    // no GPS in the photo, placed by time
	StNoTime   = "notime"   // no capture time, cannot be placed
)

// PointResult is the outcome for one photo.
type PointResult struct {
	Photo  *Photo
	Status string
	Used   bool    // used as an anchor of the track
	OffM   float64 // distance raw GPS -> placed position
	HasPos bool    // placed on the track (for waypoint export)
	Lat    float64
	Lon    float64
	Ele    float64
	HasEle bool
}

// TrackPt is a point of the generated track.
type TrackPt struct {
	Lat, Lon, Ele float64
	HasEle        bool
	Time          time.Time
}

// Result of a computation.
type Result struct {
	Points    []*PointResult
	Track     []TrackPt
	DistM     float64
	Start     time.Time
	End       time.Time
	Reversed  bool // route followed against the file's point order
	UsedRoute bool
	Warnings  []string // message codes, translated by the UI
}

// Parameters of the matcher.
const (
	stepM      = 5.0
	sigmaM     = 20.0
	vmax       = 2.0  // m/s, max plausible walking speed between two photos
	slackM     = 30.0 // extra distance allowed regardless of time
	fwdCost    = 0.0005
	backFree   = 15.0 // metres of free backward jitter
	backCost   = 0.02 // per metre of real backtracking
	snapOKM    = 60.0
	outlierM   = 200.0
	placeSlack = 15 * time.Minute
)

// ---- route geometry -----------------------------------------------------------

type geom struct {
	pts    []RoutePt
	cum    []float64
	p      proj
	L      float64
	n      int
	gx, gy []float64
	hasEle bool
}

func newGeom(pts []RoutePt) *geom {
	var lat0 float64
	for _, q := range pts {
		lat0 += q.Lat
	}
	lat0 /= float64(len(pts))
	g := &geom{pts: pts, p: newProj(lat0), hasEle: true}
	g.cum = make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		x0, y0 := g.p.xy(pts[i-1].Lat, pts[i-1].Lon)
		x1, y1 := g.p.xy(pts[i].Lat, pts[i].Lon)
		g.cum[i] = g.cum[i-1] + math.Hypot(x1-x0, y1-y0)
	}
	for _, q := range pts {
		if !q.HasEle {
			g.hasEle = false
		}
	}
	g.L = g.cum[len(pts)-1]
	g.n = int(g.L/stepM) + 1
	g.gx, g.gy = make([]float64, g.n), make([]float64, g.n)
	for k := 0; k < g.n; k++ {
		la, lo, _ := g.at(float64(k) * stepM)
		g.gx[k], g.gy[k] = g.p.xy(la, lo)
	}
	return g
}

// at returns lat, lon, ele at distance s along the route.
func (g *geom) at(s float64) (float64, float64, float64) {
	if s <= 0 {
		q := g.pts[0]
		return q.Lat, q.Lon, q.Ele
	}
	if s >= g.L {
		q := g.pts[len(g.pts)-1]
		return q.Lat, q.Lon, q.Ele
	}
	i := sort.SearchFloat64s(g.cum, s)
	if i == 0 {
		i = 1
	}
	a, b := g.pts[i-1], g.pts[i]
	seg := g.cum[i] - g.cum[i-1]
	f := 0.0
	if seg > 0 {
		f = (s - g.cum[i-1]) / seg
	}
	return a.Lat + (b.Lat-a.Lat)*f, a.Lon + (b.Lon-a.Lon)*f, a.Ele + (b.Ele-a.Ele)*f
}

type obs struct {
	x, y float64
	t    float64 // seconds since first observation
}

// viterbi map-matches observations onto the route grid.
func (g *geom) viterbi(o []obs) ([]int, float64) {
	N := g.n
	emis := func(k int, dst []float64) {
		for j := 0; j < N; j++ {
			d := math.Hypot(g.gx[j]-o[k].x, g.gy[j]-o[k].y)
			gauss := 0.5 * (d / sigmaM) * (d / sigmaM)
			capped := 6.1 + 0.02*math.Min(d, 300)
			dst[j] = math.Min(gauss, capped)
		}
	}
	cost := make([]float64, N)
	emis(0, cost)
	next := make([]float64, N)
	e := make([]float64, N)
	bp := make([][]int32, len(o))
	deque := make([]int, 0, N)
	for k := 1; k < len(o); k++ {
		dt := o[k].t - o[k-1].t
		if dt < 0 {
			dt = 0
		}
		W := int((vmax*dt + slackM) / stepM)
		if W > N {
			W = N
		}
		b := make([]int32, N)
		for j := range next {
			next[j] = math.Inf(1)
		}
		// forward moves (i <= j): cost[i] - a*S_i + a*S_j
		deque = deque[:0]
		for j := 0; j < N; j++ {
			v := cost[j] - fwdCost*float64(j)*stepM
			for len(deque) > 0 && cost[deque[len(deque)-1]]-fwdCost*float64(deque[len(deque)-1])*stepM >= v {
				deque = deque[:len(deque)-1]
			}
			deque = append(deque, j)
			for deque[0] < j-W {
				deque = deque[1:]
			}
			i := deque[0]
			c := cost[i] + fwdCost*float64(j-i)*stepM
			if c < next[j] {
				next[j], b[j] = c, int32(i)
			}
		}
		// small backward jitter (i = j+1..j+3)
		nb := int(backFree / stepM)
		for j := 0; j < N; j++ {
			for i := j + 1; i <= j+nb && i < N && i-j <= W; i++ {
				c := cost[i] + fwdCost*float64(i-j)*stepM
				if c < next[j] {
					next[j], b[j] = c, int32(i)
				}
			}
		}
		// real backtracking (i >= j+nb+1): cost[i] + a*15 + bc*(S_i - S_j - 15)
		deque = deque[:0]
		for j := N - 1; j >= 0; j-- {
			add := j + nb + 1
			if add < N && add-j <= W {
				v := cost[add] + backCost*float64(add)*stepM
				for len(deque) > 0 && cost[deque[len(deque)-1]]+backCost*float64(deque[len(deque)-1])*stepM >= v {
					deque = deque[:len(deque)-1]
				}
				deque = append(deque, add)
			}
			for len(deque) > 0 && deque[0] > j+W {
				deque = deque[1:]
			}
			if len(deque) > 0 {
				i := deque[0]
				c := cost[i] + fwdCost*backFree + backCost*(float64(i-j)*stepM-backFree)
				if c < next[j] {
					next[j], b[j] = c, int32(i)
				}
			}
		}
		emis(k, e)
		for j := 0; j < N; j++ {
			cost[j] = next[j] + e[j]
		}
		bp[k] = b
	}
	best, bi := math.Inf(1), 0
	for j, c := range cost {
		if c < best {
			best, bi = c, j
		}
	}
	st := make([]int, len(o))
	st[len(o)-1] = bi
	for k := len(o) - 1; k >= 1; k-- {
		st[k-1] = int(bp[k][st[k]])
	}
	return st, best
}

// ---- main computation ---------------------------------------------------------

func coordKey(p *Photo) [2]int64 {
	return [2]int64{int64(math.Round(p.Lat * 1e6)), int64(math.Round(p.Lon * 1e6))}
}

// detectCached flags GPS coordinates that re-appear later after the phone had
// moved elsewhere: typical for stale cached fixes reused by the camera app.
func detectCached(ph []*Photo) map[[2]int64]bool {
	last := map[[2]int64]time.Time{}
	stale := map[[2]int64]bool{}
	var prev [2]int64
	hasPrev := false
	for _, p := range ph {
		if !p.HasGPS || !p.HasTime {
			continue
		}
		k := coordKey(p)
		if t, ok := last[k]; ok && (!hasPrev || prev != k) && p.Time.Sub(t) > 5*time.Minute {
			stale[k] = true
		}
		last[k] = p.Time
		prev, hasPrev = k, true
	}
	return stale
}

// Compute builds the track. dir: "auto", "forward" or "reverse".
func Compute(photos []*Photo, route *Route, ov map[string]Override, dir string) *Result {
	res := &Result{}
	ph := append([]*Photo(nil), photos...)
	sort.SliceStable(ph, func(i, j int) bool {
		a, b := ph[i], ph[j]
		if a.HasTime != b.HasTime {
			return a.HasTime
		}
		if !a.Time.Equal(b.Time) {
			return a.Time.Before(b.Time)
		}
		return a.Name < b.Name
	})
	byID := map[string]*PointResult{}
	for _, p := range ph {
		pr := &PointResult{Photo: p}
		byID[p.ID] = pr
		res.Points = append(res.Points, pr)
	}
	stale := detectCached(ph)

	// candidates for anchoring
	var cand []*PointResult
	for _, pr := range res.Points {
		p := pr.Photo
		switch {
		case !p.HasTime:
			pr.Status = StNoTime
		case !p.HasGPS:
			pr.Status = StNoGPS
		case ov[p.ID] == ForceExclude:
			pr.Status = StExcluded
		case stale[coordKey(p)] && ov[p.ID] != ForceInclude:
			pr.Status = StCached
		default:
			cand = append(cand, pr)
		}
	}
	if len(cand) < 2 {
		if len(ph) > 0 {
			res.Warnings = append(res.Warnings, "tooFewGPS")
		}
		for _, pr := range cand {
			pr.Status = StOK
		}
		return res
	}
	t0 := cand[0].Photo.Time

	type anchor struct {
		t        time.Time
		s        float64 // route mode
		lat, lon float64 // no-route mode
		ele      float64
		hasEle   bool
	}
	var anchors []anchor
	var g *geom

	if route != nil && len(route.Pts) >= 2 {
		res.UsedRoute = true
		run := func(rev bool) (*geom, []int, float64) {
			pts := append([]RoutePt(nil), route.Pts...)
			if rev {
				for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
					pts[i], pts[j] = pts[j], pts[i]
				}
			}
			gg := newGeom(pts)
			o := make([]obs, len(cand))
			for i, pr := range cand {
				x, y := gg.p.xy(pr.Photo.Lat, pr.Photo.Lon)
				o[i] = obs{x: x, y: y, t: pr.Photo.Time.Sub(t0).Seconds()}
			}
			st, c := gg.viterbi(o)
			return gg, st, c
		}
		var st []int
		switch dir {
		case "forward":
			g, st, _ = run(false)
		case "reverse":
			g, st, _ = run(true)
			res.Reversed = true
		default:
			gf, sf, cf := run(false)
			gr, sr, cr := run(true)
			if cr < cf {
				g, st, res.Reversed = gr, sr, true
			} else {
				g, st = gf, sf
			}
		}
		for i, pr := range cand {
			s := float64(st[i]) * stepM
			la, lo, el := g.at(s)
			pr.OffM = HaversineM(pr.Photo.Lat, pr.Photo.Lon, la, lo)
			if pr.OffM > outlierM && ov[pr.Photo.ID] != ForceInclude {
				pr.Status = StOutlier
				continue
			}
			pr.Used, pr.HasPos = true, true
			pr.Lat, pr.Lon, pr.Ele, pr.HasEle = la, lo, el, g.hasEle
			if pr.OffM <= snapOKM {
				pr.Status = StOK
			} else {
				pr.Status = StSnapped
			}
			anchors = append(anchors, anchor{t: pr.Photo.Time, s: s})
		}
	} else {
		// no reference route: connect photos; drop impossible jumps
		used := make([]bool, len(cand))
		speed := func(a, b *Photo) (float64, float64) {
			d := HaversineM(a.Lat, a.Lon, b.Lat, b.Lon)
			dt := b.Time.Sub(a.Time).Seconds()
			return d, (d - slackM) / math.Max(dt, 1)
		}
		lastOK := -1
		for i, pr := range cand {
			if ov[pr.Photo.ID] == ForceInclude || lastOK < 0 {
				used[i], lastOK = true, i
				continue
			}
			prev := cand[lastOK].Photo
			d, v := speed(prev, pr.Photo)
			bad := d > 150 && v > vmax*1.5
			if i+1 < len(cand) {
				nx := cand[i+1].Photo
				d2, v2 := speed(pr.Photo, nx)
				if bad {
					// confirm with the next point: a real move would be consistent
					bad = d2 > 150 && v2 > vmax*1.5
				}
				// spike: jumps far away and comes straight back
				direct := math.Max(HaversineM(prev.Lat, prev.Lon, nx.Lat, nx.Lon), 50)
				if math.Min(d, d2) > 200 && d+d2-direct > 300 && (d+d2)/direct > 4 {
					bad = true
				}
			}
			if bad {
				pr.Status = StOutlier
				continue
			}
			used[i], lastOK = true, i
		}
		for i, pr := range cand {
			if !used[i] {
				continue
			}
			p := pr.Photo
			pr.Used, pr.HasPos, pr.Status = true, true, StOK
			pr.Lat, pr.Lon, pr.Ele, pr.HasEle = p.Lat, p.Lon, p.Alt, p.HasAlt
			anchors = append(anchors, anchor{t: p.Time, lat: p.Lat, lon: p.Lon, ele: p.Alt, hasEle: p.HasAlt})
		}
	}
	if len(anchors) < 2 {
		res.Warnings = append(res.Warnings, "tooFewValid")
		return res
	}

	// ---- build track ---------------------------------------------------------
	add := func(tp TrackPt) {
		if n := len(res.Track); n > 0 {
			l := res.Track[n-1]
			if l.Lat == tp.Lat && l.Lon == tp.Lon && tp.Time.Sub(l.Time) < time.Minute {
				return
			}
		}
		res.Track = append(res.Track, tp)
	}
	if g != nil {
		for k := 0; k < len(anchors); k++ {
			a := anchors[k]
			if k == 0 {
				la, lo, el := g.at(a.s)
				add(TrackPt{la, lo, el, g.hasEle, a.t})
				continue
			}
			p := anchors[k-1]
			lo, hi := math.Min(p.s, a.s), math.Max(p.s, a.s)
			var mids []float64
			for _, c := range g.cum {
				if c > lo && c < hi {
					mids = append(mids, c)
				}
			}
			if a.s < p.s {
				for i, j := 0, len(mids)-1; i < j; i, j = i+1, j-1 {
					mids[i], mids[j] = mids[j], mids[i]
				}
			}
			span := a.t.Sub(p.t)
			for _, c := range mids {
				f := math.Abs(c-p.s) / math.Abs(a.s-p.s)
				la, lon, el := g.at(c)
				add(TrackPt{la, lon, el, g.hasEle, p.t.Add(time.Duration(f * float64(span)))})
			}
			la, lon, el := g.at(a.s)
			add(TrackPt{la, lon, el, g.hasEle, a.t})
			res.DistM += math.Abs(a.s - p.s)
		}
	} else {
		for k, a := range anchors {
			add(TrackPt{a.lat, a.lon, a.ele, a.hasEle, a.t})
			if k > 0 {
				res.DistM += HaversineM(anchors[k-1].lat, anchors[k-1].lon, a.lat, a.lon)
			}
		}
	}
	res.Start, res.End = res.Track[0].Time, res.Track[len(res.Track)-1].Time

	// ---- place the remaining photos by time -----------------------------------
	posAt := func(t time.Time) (float64, float64, float64, bool) {
		tr := res.Track
		if !t.After(tr[0].Time) {
			return tr[0].Lat, tr[0].Lon, tr[0].Ele, tr[0].HasEle
		}
		if !t.Before(tr[len(tr)-1].Time) {
			l := tr[len(tr)-1]
			return l.Lat, l.Lon, l.Ele, l.HasEle
		}
		i := sort.Search(len(tr), func(i int) bool { return tr[i].Time.After(t) })
		a, b := tr[i-1], tr[i]
		f := 0.0
		if d := b.Time.Sub(a.Time); d > 0 {
			f = float64(t.Sub(a.Time)) / float64(d)
		}
		return a.Lat + (b.Lat-a.Lat)*f, a.Lon + (b.Lon-a.Lon)*f, a.Ele + (b.Ele-a.Ele)*f, a.HasEle && b.HasEle
	}
	for _, pr := range res.Points {
		p := pr.Photo
		if pr.Used || !p.HasTime {
			continue
		}
		if p.Time.Before(res.Start.Add(-placeSlack)) || p.Time.After(res.End.Add(placeSlack)) {
			continue // outside the walk; not placed
		}
		pr.Lat, pr.Lon, pr.Ele, pr.HasEle = posAt(p.Time)
		pr.HasPos = true
		if p.HasGPS {
			pr.OffM = HaversineM(p.Lat, p.Lon, pr.Lat, pr.Lon)
		}
	}
	return res
}

// RouteLatLon returns the reference route as [lat, lon] pairs (for display).
func RouteLatLon(r *Route) [][2]float64 {
	out := make([][2]float64, len(r.Pts))
	for i, p := range r.Pts {
		out[i] = [2]float64{p.Lat, p.Lon}
	}
	return out
}

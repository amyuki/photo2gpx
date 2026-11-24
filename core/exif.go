package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// ExifInfo holds the few EXIF fields this tool needs.
type ExifInfo struct {
	HasGPS      bool
	Lat, Lon    float64
	Alt         float64
	HasAlt      bool
	LocalTime   string // "2006:01:02 15:04:05" as written by the camera (no zone)
	Offset      string // "+02:00" (OffsetTimeOriginal / OffsetTime), may be empty
	GPSTimeUTC  time.Time
	HasGPSTime  bool
	Model       string
	Thumb       []byte // embedded JPEG thumbnail, if any
	Orientation int    // EXIF orientation 1-8 (0 = not set)
}

// ReadExif reads the EXIF block of a JPEG file. Only the first segments are read.
func ReadExif(path string) (*ExifInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// EXIF lives in APP1 near the start; read the header area only.
	buf := make([]byte, 256*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	buf = buf[:n]
	tiff, err := findExifTIFF(buf)
	if err != nil {
		return nil, err
	}
	return parseTIFF(tiff)
}

func findExifTIFF(b []byte) ([]byte, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, errors.New("not a JPEG file")
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return nil, errors.New("malformed JPEG")
		}
		marker := b[i+1]
		if marker == 0xD9 || marker == 0xDA { // EOI / start of scan
			break
		}
		segLen := int(binary.BigEndian.Uint16(b[i+2:]))
		start, end := i+4, i+2+segLen
		if end > len(b) {
			end = len(b)
		}
		if marker == 0xE1 && end-start > 6 && bytes.Equal(b[start:start+6], []byte("Exif\x00\x00")) {
			return b[start+6 : end], nil
		}
		i = i + 2 + segLen
	}
	return nil, errors.New("no EXIF data")
}

type tiffReader struct {
	b  []byte
	bo binary.ByteOrder
}

type ifdEntry struct {
	tag, typ uint16
	count    uint32
	valOff   uint32 // raw 4 bytes interpreted as offset
	raw      []byte // the 4 raw value bytes
}

func (t *tiffReader) ifd(off uint32) ([]ifdEntry, uint32) {
	if int(off)+2 > len(t.b) {
		return nil, 0
	}
	n := int(t.bo.Uint16(t.b[off:]))
	var out []ifdEntry
	p := int(off) + 2
	for k := 0; k < n; k++ {
		if p+12 > len(t.b) {
			break
		}
		e := ifdEntry{
			tag:   t.bo.Uint16(t.b[p:]),
			typ:   t.bo.Uint16(t.b[p+2:]),
			count: t.bo.Uint32(t.b[p+4:]),
			raw:   t.b[p+8 : p+12],
		}
		e.valOff = t.bo.Uint32(t.b[p+8:])
		out = append(out, e)
		p += 12
	}
	var next uint32
	if p+4 <= len(t.b) {
		next = t.bo.Uint32(t.b[p:])
	}
	return out, next
}

var typeSize = map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1, 9: 4, 10: 8}

func (t *tiffReader) data(e ifdEntry) []byte {
	sz := typeSize[e.typ] * int(e.count)
	if sz <= 4 {
		return e.raw[:sz]
	}
	if int(e.valOff)+sz > len(t.b) {
		return nil
	}
	return t.b[e.valOff : int(e.valOff)+sz]
}

func (t *tiffReader) str(e ifdEntry) string {
	return strings.TrimRight(string(t.data(e)), "\x00 ")
}

func (t *tiffReader) rationals(e ifdEntry) []float64 {
	d := t.data(e)
	var out []float64
	for i := 0; i+8 <= len(d); i += 8 {
		num, den := t.bo.Uint32(d[i:]), t.bo.Uint32(d[i+4:])
		if den == 0 {
			out = append(out, math.NaN())
		} else {
			out = append(out, float64(num)/float64(den))
		}
	}
	return out
}

func (t *tiffReader) uint(e ifdEntry) uint32 {
	switch e.typ {
	case 3:
		return uint32(t.bo.Uint16(e.raw))
	default:
		return t.bo.Uint32(e.raw)
	}
}

func parseTIFF(b []byte) (*ExifInfo, error) {
	if len(b) < 8 {
		return nil, errors.New("EXIF block too short")
	}
	t := &tiffReader{b: b}
	switch string(b[:2]) {
	case "II":
		t.bo = binary.LittleEndian
	case "MM":
		t.bo = binary.BigEndian
	default:
		return nil, errors.New("unknown EXIF byte order")
	}
	info := &ExifInfo{}
	ifd0, next := t.ifd(t.bo.Uint32(b[4:]))
	var exifOff, gpsOff uint32
	var dateTime string
	for _, e := range ifd0 {
		switch e.tag {
		case 0x0112:
			info.Orientation = int(t.uint(e))
		case 0x0110:
			info.Model = t.str(e)
		case 0x0132:
			dateTime = t.str(e)
		case 0x8769:
			exifOff = t.uint(e)
		case 0x8825:
			gpsOff = t.uint(e)
		}
	}
	var offOrig, offAny string
	if exifOff != 0 {
		ents, _ := t.ifd(exifOff)
		for _, e := range ents {
			switch e.tag {
			case 0x9003:
				info.LocalTime = t.str(e)
			case 0x9011:
				offOrig = t.str(e)
			case 0x9010:
				offAny = t.str(e)
			}
		}
	}
	if info.LocalTime == "" {
		info.LocalTime = dateTime
	}
	info.Offset = offOrig
	if info.Offset == "" {
		info.Offset = offAny
	}
	if gpsOff != 0 {
		ents, _ := t.ifd(gpsOff)
		var latRef, lonRef string
		var lat, lon, alt, ts []float64
		var altRef byte
		var date string
		for _, e := range ents {
			switch e.tag {
			case 1:
				latRef = t.str(e)
			case 2:
				lat = t.rationals(e)
			case 3:
				lonRef = t.str(e)
			case 4:
				lon = t.rationals(e)
			case 5:
				if d := t.data(e); len(d) > 0 {
					altRef = d[0]
				}
			case 6:
				alt = t.rationals(e)
			case 7:
				ts = t.rationals(e)
			case 29:
				date = t.str(e)
			}
		}
		if len(lat) == 3 && len(lon) == 3 {
			la := lat[0] + lat[1]/60 + lat[2]/3600
			lo := lon[0] + lon[1]/60 + lon[2]/3600
			if latRef == "S" {
				la = -la
			}
			if lonRef == "W" {
				lo = -lo
			}
			if !math.IsNaN(la) && !math.IsNaN(lo) && !(la == 0 && lo == 0) {
				info.HasGPS, info.Lat, info.Lon = true, la, lo
			}
		}
		if len(alt) == 1 && !math.IsNaN(alt[0]) {
			info.Alt, info.HasAlt = alt[0], true
			if altRef == 1 {
				info.Alt = -info.Alt
			}
		}
		if len(ts) == 3 && date != "" {
			if d, err := time.Parse("2006:01:02", date); err == nil {
				sec := ts[0]*3600 + ts[1]*60 + ts[2]
				if !math.IsNaN(sec) {
					info.GPSTimeUTC = d.Add(time.Duration(sec * float64(time.Second)))
					info.HasGPSTime = true
				}
			}
		}
	}
	// IFD1: embedded thumbnail
	if next != 0 {
		ents, _ := t.ifd(next)
		var toff, tlen uint32
		for _, e := range ents {
			switch e.tag {
			case 0x0201:
				toff = t.uint(e)
			case 0x0202:
				tlen = t.uint(e)
			}
		}
		if toff > 0 && tlen > 0 && int(toff+tlen) <= len(b) {
			info.Thumb = append([]byte(nil), b[toff:toff+tlen]...)
		}
	}
	return info, nil
}

// ParseOffset parses "+02:00" into seconds east of UTC.
func ParseOffset(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	parts := strings.SplitN(s[1:], ":", 2)
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	m := 0
	if len(parts) == 2 {
		if m, err = strconv.Atoi(parts[1]); err != nil {
			return 0, false
		}
	}
	sec := h*3600 + m*60
	if s[0] == '-' {
		sec = -sec
	}
	return sec, true
}

// ParseLocal parses the EXIF local time (without zone) as if it were UTC.
func ParseLocal(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	t, err := time.Parse("2006:01:02 15:04:05", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse capture time %q", s)
	}
	return t, nil
}

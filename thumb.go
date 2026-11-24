package main

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"

	"photo2gpx/core"

	"golang.org/x/image/draw"
)

const thumbW, thumbH = 480, 320 // fits the 240×160 popup at 2× for high-DPI screens

// makeThumb returns a JPEG preview of a photo, upright and sharp enough for
// the map popup. The small preview embedded in the EXIF data is used when it
// is big enough; otherwise the full image is decoded.
func makeThumb(p *core.Photo) []byte {
	var img image.Image
	if len(p.Thumb) > 0 {
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(p.Thumb)); err == nil && max(cfg.Width, cfg.Height) >= thumbW {
			img, _ = jpeg.Decode(bytes.NewReader(p.Thumb))
		}
	}
	if img == nil {
		if f, err := os.Open(p.Path); err == nil {
			img, err = jpeg.Decode(f)
			f.Close()
			if err != nil {
				img = nil
			}
		}
	}
	if img == nil && len(p.Thumb) > 0 { // full image unreadable: small preview is better than nothing
		img, _ = jpeg.Decode(bytes.NewReader(p.Thumb))
	}
	if img == nil {
		return nil
	}
	img = orient(img, p.Orientation)
	b := img.Bounds()
	// cover the popup frame (it crops with object-fit: cover)
	scale := min(max(float64(thumbW)/float64(b.Dx()), float64(thumbH)/float64(b.Dy())), 1)
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// orient applies the EXIF orientation so the picture is upright.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 { // 90° rotations swap the sides
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

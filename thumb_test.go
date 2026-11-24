package main

import (
	"image"
	"image/color"
	"testing"
)

func TestOrient(t *testing.T) {
	// 2×1 image: red at left, blue at right
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{0, 0, 255, 255})
	red := func(img image.Image, x, y int) bool { r, _, _, _ := img.At(x, y).RGBA(); return r > 0 }
	if o := orient(src, 6); o.Bounds().Dx() != 1 || o.Bounds().Dy() != 2 || !red(o, 0, 0) {
		t.Fatal("orientation 6: left edge should become the top")
	}
	if o := orient(src, 8); !red(o, 0, 1) {
		t.Fatal("orientation 8: left edge should become the bottom")
	}
	if o := orient(src, 3); !red(o, 1, 0) {
		t.Fatal("orientation 3: rotated 180")
	}
}

package web

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// icon draws the home screen icon at size×size: the pad's black screen
// with a 3×2 grid of keys, one lit green for ready and one ochre for an
// agent that needs you.
func icon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := func(r image.Rectangle, c color.RGBA) { draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src) }
	fill(img.Bounds(), color.RGBA{0x0f, 0x0e, 0x12, 0xff})

	key := color.RGBA{0x3a, 0x39, 0x3f, 0xff}
	lit := map[int]color.RGBA{0: {0x1e, 0x8a, 0x4c, 0xff}, 4: {0xfa, 0xb4, 0x13, 0xff}}
	margin, gap := size*18/100, size*5/100
	w := (size - 2*margin - 2*gap) / 3
	h := w * 3 / 4
	top := (size - 2*h - gap) / 2
	for i := range 6 {
		x := margin + (i%3)*(w+gap)
		y := top + (i/3)*(h+gap)
		c, ok := lit[i]
		if !ok {
			c = key
		}
		fill(image.Rect(x, y, x+w, y+h), c)
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

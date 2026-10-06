package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// icon draws a crisp two-way transfer mark at 2x menu-bar resolution. A solid
// background on non-template icons keeps it legible on either taskbar theme.
func icon(template, attention bool) []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	ink := color.NRGBA{R: 240, G: 245, B: 242, A: 255}
	if template {
		ink = color.NRGBA{A: 255}
	} else {
		for y := 2; y < 34; y++ {
			for x := 2; x < 34; x++ {
				im.SetNRGBA(x, y, color.NRGBA{R: 40, G: 76, B: 63, A: 255})
			}
		}
	}
	rect := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				im.SetNRGBA(x, y, ink)
			}
		}
	}
	rect(8, 11, 27, 14)
	rect(9, 22, 28, 25)
	for n := 0; n < 7; n++ {
		rect(24-n, 7+n, 27-n, 10+n)
		rect(9+n, 20+n, 12+n, 23+n)
	}
	if attention {
		for y := 1; y < 8; y++ {
			for x := 28; x < 35; x++ {
				im.SetNRGBA(x, y, ink)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}

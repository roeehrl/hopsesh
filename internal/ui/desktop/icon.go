package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

const (
	trayCanvas = 24.0
	// Wails v3 scales the entire PNG to NSStatusBar.thickness, not to 18 pt.
	// Supply a 2x 24 pt canvas with an 18 pt wide mark and transparent padding.
	trayIconPixels = 48
	logoScale      = 18.0 / 488.0
)

type iconPoint struct{ x, y float64 }

// The foreground of docs/assets/logo.svg: M330 640 A182 230 0 0 1 694 640,
// stroke width 40 with round caps; circles at (330,680), (694,680), radius 62.
// Its ink bounds are (268,390)-(756,742). Center those bounds in the tray canvas
// instead of shrinking the app icon's background and large outer margins too.
var logoArc = func() [97]iconPoint {
	var points [97]iconPoint
	for i := range points {
		t := math.Pi + math.Pi*float64(i)/float64(len(points)-1)
		points[i] = iconPoint{512 + 182*math.Cos(t), 640 + 230*math.Sin(t)}
	}
	return points
}()

func icon(template, attention bool) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, renderTrayIcon(trayIconPixels, template, attention))
	return b.Bytes()
}

// renderTrayIcon keeps partial alpha at curved edges. Template pixels have no
// colour or background: AppKit supplies the light, dark and selected appearance.
func renderTrayIcon(size int, template, attention bool) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	fg, bg := color.NRGBA{246, 245, 241, 255}, color.NRGBA{11, 107, 98, 255}
	if template {
		fg = color.NRGBA{A: 255}
	}
	const samples = 8
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, hits int
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := (float64(x) + (float64(sx)+0.5)/samples) * trayCanvas / float64(size)
					py := (float64(y) + (float64(sy)+0.5)/samples) * trayCanvas / float64(size)
					c := fg
					// A small, separate dot preserves both terminals of the logo. In
					// template mode it is also monochrome, never a coloured badge.
					badge := attention && iconCircle(px, py, 20.5, 3.5, 1.25)
					switch {
					case badge:
						if !template {
							c = color.NRGBA{245, 188, 84, 255}
						}
					case onLogo((px-12)/logoScale+512, (py-12)/logoScale+566):
					case !template && iconBackground(px, py):
						c = bg
					default:
						continue
					}
					r, g, b, hits = r+int(c.R), g+int(c.G), b+int(c.B), hits+1
				}
			}
			if hits > 0 {
				im.SetNRGBA(x, y, color.NRGBA{uint8(r / hits), uint8(g / hits), uint8(b / hits), uint8((255*hits + samples*samples/2) / (samples * samples))})
			}
		}
	}
	return im
}

func onLogo(x, y float64) bool {
	if iconCircle(x, y, 330, 680, 62) || iconCircle(x, y, 694, 680, 62) {
		return true
	}
	if x < 310 || x > 714 || y < 390 || y > 660 {
		return false
	}
	// Distance to a finely tessellated ellipse gives a constant-width stroke,
	// including round end caps. Normalized ellipse radii would distort its weight.
	for i := 1; i < len(logoArc); i++ {
		a, b := logoArc[i-1], logoArc[i]
		dx, dy := b.x-a.x, b.y-a.y
		t := max(0, min(1, ((x-a.x)*dx+(y-a.y)*dy)/(dx*dx+dy*dy)))
		if iconCircle(x, y, a.x+t*dx, a.y+t*dy, 20) {
			return true
		}
	}
	return false
}

func iconCircle(x, y, cx, cy, r float64) bool {
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

func iconBackground(x, y float64) bool {
	if x < 1 || y < 1 || x > 23 || y > 23 {
		return false
	}
	return iconCircle(x, y, max(7, min(x, 17)), max(7, min(y, 17)), 6)
}

// Command icon renders hopsesh's app icon (1024×1024 PNG): a teal rounded square with a
// "hop" arc between two dots. Kept in code so the repository has no binary design assets.
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	const n = 1024
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	bg := color.NRGBA{11, 107, 98, 255}
	fg := color.NRGBA{246, 245, 241, 255}
	r := 220.0 // corner radius
	margin := 100.0
	inRounded := func(x, y float64) bool {
		lo, hi := margin, n-margin
		if x < lo || x > hi || y < lo || y > hi {
			return false
		}
		cx := math.Max(lo+r, math.Min(x, hi-r))
		cy := math.Max(lo+r, math.Min(y, hi-r))
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
	}
	// arc: upper half of an ellipse from left dot to right dot
	ax, bx, base := 330.0, 694.0, 640.0
	rx, ry := (bx-ax)/2, 230.0
	cxA := (ax + bx) / 2
	thick := 34.0
	onArc := func(x, y float64) bool {
		if y > base {
			return false
		}
		dx, dy := (x-cxA)/rx, (y-base)/ry
		d := math.Sqrt(dx*dx + dy*dy)
		return math.Abs(d-1)*math.Min(rx, ry) < thick/2
	}
	dot := func(x, y, cx, cy, rad float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad }
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			// 4x supersampling for smooth edges
			var bgHits, fgHits int
			for sy := 0; sy < 2; sy++ {
				for sx := 0; sx < 2; sx++ {
					fx, fy := float64(x)+0.25+0.5*float64(sx), float64(y)+0.25+0.5*float64(sy)
					if !inRounded(fx, fy) {
						continue
					}
					if onArc(fx, fy) || dot(fx, fy, ax, base+40, 62) || dot(fx, fy, bx, base+40, 62) {
						fgHits++
					} else {
						bgHits++
					}
				}
			}
			if bgHits+fgHits == 0 {
				continue
			}
			c := bg
			if fgHits > 0 {
				t := float64(fgHits) / float64(bgHits+fgHits)
				c = color.NRGBA{uint8(float64(bg.R)*(1-t) + float64(fg.R)*t), uint8(float64(bg.G)*(1-t) + float64(fg.G)*t), uint8(float64(bg.B)*(1-t) + float64(fg.B)*t), 255}
			}
			c.A = uint8(255 * (bgHits + fgHits) / 4)
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

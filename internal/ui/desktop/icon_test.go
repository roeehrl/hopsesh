package desktop

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestTrayIconTemplate(t *testing.T) {
	decoded, err := png.Decode(bytes.NewReader(icon(true, false)))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != image.Rect(0, 0, 48, 48) {
		t.Fatalf("expected a 2x 24 pt canvas: %v", decoded.Bounds())
	}
	for _, size := range []int{18, 22, 24, 36, 44, 48} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			im := renderTrayIcon(size, true, false)
			partial := 0
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					c := im.NRGBAAt(x, y)
					if c.R != 0 || c.G != 0 || c.B != 0 {
						t.Fatal("template has colour instead of an alpha mask")
					}
					if (x == 0 || y == 0 || x == size-1 || y == size-1) && c.A != 0 {
						t.Fatal("mark touches the canvas edge")
					}
					if c.A > 0 && c.A < 255 {
						partial++
					}
					mirror := im.NRGBAAt(size-1-x, y).A
					if abs(int(c.A)-int(mirror)) > 4 {
						t.Fatal("normal logo is not symmetric")
					}
				}
			}
			if partial == 0 {
				t.Fatal("curves are not antialiased")
			}
			// Recognizable foreground features, in the 24 pt canvas: arch apex,
			// two substantial terminal dots, and an open centre (not arrows).
			for _, p := range []image.Point{{12, 6}, {5, 16}, {18, 16}} {
				if im.NRGBAAt(p.X*size/24, p.Y*size/24).A < 100 {
					t.Fatalf("logo feature absent at %v", p)
				}
			}
			if im.NRGBAAt(size/2, size/2).A != 0 {
				t.Fatal("arch centre must remain transparent")
			}
		})
	}
}

func TestTrayIconAttentionPreservesLogo(t *testing.T) {
	for _, template := range []bool{true, false} {
		normal, attention := renderTrayIcon(48, template, false), renderTrayIcon(48, template, true)
		changed, normalAlpha, addedAlpha := 0, 0, 0
		for y := 0; y < 48; y++ {
			for x := 0; x < 48; x++ {
				a, b := normal.NRGBAAt(x, y), attention.NRGBAAt(x, y)
				normalAlpha += int(a.A)
				if a != b {
					changed++
					if x < 38 || y >= 10 {
						t.Fatalf("attention changed the logo outside the small corner badge: %d,%d", x, y)
					}
					if template && (a.A != 0 || b.R != 0 || b.G != 0 || b.B != 0) {
						t.Fatal("template attention obscures or colours the original logo")
					}
					addedAlpha += int(b.A) - int(a.A)
				}
			}
		}
		if changed == 0 || changed > 36 || template && addedAlpha > normalAlpha/10 {
			t.Fatalf("attention badge missing or too prominent: %d pixels", changed)
		}
	}
}

func TestTrayIconBrandColours(t *testing.T) {
	im := renderTrayIcon(48, false, false)
	if got := im.NRGBAAt(24, 24); got != (color.NRGBA{11, 107, 98, 255}) {
		t.Fatalf("expected official teal background, got %v", got)
	}
	if got := im.NRGBAAt(11, 32); got != (color.NRGBA{246, 245, 241, 255}) {
		t.Fatalf("expected official light foreground, got %v", got)
	}
	if im.NRGBAAt(0, 0).A != 0 {
		t.Fatal("rounded background needs transparent corners")
	}
}

// Optional preview export, using the production rasterizer. No app is built or
// launched. HOPSESH_ICON_PREVIEW_DIR=/tmp/hopsesh-tray go test ./internal/ui/desktop -run TestTrayIconPreviews
func TestTrayIconPreviews(t *testing.T) {
	dir := os.Getenv("HOPSESH_ICON_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set HOPSESH_ICON_PREVIEW_DIR to export native-size PNGs")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, 760, 620))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	label := func(x, y int, text string, ink color.Color) {
		d := font.Drawer{Dst: sheet, Src: image.NewUniform(ink), Face: basicfont.Face7x13, Dot: fixed.P(x, y)}
		d.DrawString(text)
	}
	label(12, 20, "Hopsesh tray: native pixels / 1x (left) and 2x (right)", color.Black)
	sizes := []int{18, 22, 24, 36, 44, 48}
	for col, size := range sizes {
		label(178+col*96, 42, fmt.Sprintf("%d px", size), color.Black)
	}
	styles := []struct {
		name     string
		bg, ink  color.NRGBA
		template bool
	}{
		{"Light", color.NRGBA{246, 245, 241, 255}, color.NRGBA{30, 30, 30, 255}, true},
		{"Dark", color.NRGBA{40, 42, 45, 255}, color.NRGBA{246, 245, 241, 255}, true},
		{"Selected", color.NRGBA{20, 98, 200, 255}, color.NRGBA{255, 255, 255, 255}, true},
		{"Branded", color.NRGBA{246, 245, 241, 255}, color.NRGBA{30, 30, 30, 255}, false},
	}
	for row, style := range styles {
		for state, attention := range []bool{false, true} {
			y := 52 + (row*2+state)*70
			draw.Draw(sheet, image.Rect(0, y, 760, y+70), image.NewUniform(style.bg), image.Point{}, draw.Src)
			name := "normal"
			if attention {
				name = "attention"
			}
			label(12, y+38, style.name+" / "+name, style.ink)
			for col, size := range sizes {
				im := renderTrayIcon(size, style.template, attention)
				writeIconPNG(t, filepath.Join(dir, fmt.Sprintf("%s-%s-%dpx.png", style.name, name, size)), im)
				if style.template {
					// Simulate AppKit tinting the alpha mask; production stays black.
					for py := 0; py < size; py++ {
						for px := 0; px < size; px++ {
							c := style.ink
							c.A = im.NRGBAAt(px, py).A
							im.SetNRGBA(px, py, c)
						}
					}
				}
				draw.Draw(sheet, image.Rect(198+col*96-size/2, y+35-size/2, 198+col*96+(size+1)/2, y+35+(size+1)/2), im, image.Point{}, draw.Over)
			}
		}
	}
	writeIconPNG(t, filepath.Join(dir, "tray-preview.png"), sheet)
}

func writeIconPNG(t *testing.T, path string, im image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

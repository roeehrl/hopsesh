package appicon

import (
	"bytes"
	"encoding/binary"
	"errors"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// PNGFromICNS picks the PNG in a macOS .icns file whose width is nearest to want (the
// smallest one at least that wide, else the widest).
func PNGFromICNS(b []byte, want int) ([]byte, error) {
	if len(b) < 8 || string(b[:4]) != "icns" {
		return nil, errors.New("not an icns file")
	}
	var best []byte
	bestW := 0
	for i := 8; i+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i+4 : i+8]))
		if n < 8 || i+n > len(b) {
			break
		}
		d := b[i+8 : i+n]
		i += n
		if len(d) < 24 || !bytes.HasPrefix(d, pngMagic) {
			continue // older formats (ARGB, JPEG 2000) are not used
		}
		w := int(binary.BigEndian.Uint32(d[16:20])) // IHDR width
		better := best == nil ||
			w >= want && (bestW < want || w < bestW) ||
			w < want && bestW < want && w > bestW
		if better {
			best, bestW = d, w
		}
	}
	if best == nil {
		return nil, errors.New("the icns file has no PNG image")
	}
	return best, nil
}

package appicon

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// fakePNG is enough of a PNG for the reader: the signature and an IHDR with a width.
func fakePNG(w int) []byte {
	b := append([]byte{}, pngMagic...)
	b = append(b, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	b = binary.BigEndian.AppendUint32(b, uint32(w))
	return binary.BigEndian.AppendUint32(b, uint32(w))
}

func icns(entries map[string][]byte, order ...string) []byte {
	var body []byte
	for _, t := range order {
		d := entries[t]
		body = append(body, t...)
		body = binary.BigEndian.AppendUint32(body, uint32(8+len(d)))
		body = append(body, d...)
	}
	out := []byte("icns")
	out = binary.BigEndian.AppendUint32(out, uint32(8+len(body)))
	return append(out, body...)
}

func TestPNGFromICNS(t *testing.T) {
	e := map[string][]byte{"ic10": fakePNG(1024), "ic04": []byte("ARGB...."), "ic08": fakePNG(256), "ic07": fakePNG(128)}
	f := icns(e, "ic10", "ic04", "ic08", "ic07")
	for want, got := range map[int][]byte{128: e["ic07"], 200: e["ic08"], 2048: e["ic10"], 16: e["ic07"]} {
		if b, err := PNGFromICNS(f, want); err != nil || !bytes.Equal(b, got) {
			t.Errorf("want %d: %v", want, err)
		}
	}
	if _, err := PNGFromICNS(icns(map[string][]byte{"ic04": []byte("ARGB....")}, "ic04"), 128); err == nil {
		t.Error("an icns without PNGs has no icon")
	}
	if _, err := PNGFromICNS([]byte("nope"), 128); err == nil {
		t.Error("not an icns")
	}
}

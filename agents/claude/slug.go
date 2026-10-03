package claude

import (
	"strconv"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// maxSlugLen is the length (in UTF-16 code units) after which Claude Code truncates a
// project folder name and appends a hash of the full path.
const maxSlugLen = 200

// Slug returns the project folder name Claude Code uses for a resolved working directory:
// every UTF-16 code unit that is not [A-Za-z0-9] becomes '-', and names longer than 200
// units are cut to 200 and suffixed with "-" plus the base-36 absolute value of the Java
// String.hashCode of the full path. The path is NFC-normalised first, as Claude Code does.
func Slug(path string) string {
	p := norm.NFC.String(path)
	units := utf16.Encode([]rune(p))
	out := make([]byte, len(units))
	for i, u := range units {
		switch {
		case u >= 'a' && u <= 'z', u >= 'A' && u <= 'Z', u >= '0' && u <= '9':
			out[i] = byte(u)
		default:
			out[i] = '-'
		}
	}
	if len(out) <= maxSlugLen {
		return string(out)
	}
	return string(out[:maxSlugLen]) + "-" + strconv.FormatInt(abs64(int64(javaHash(units))), 36)
}

// javaHash is Java's String.hashCode over UTF-16 code units (32-bit wrapping).
func javaHash(units []uint16) int32 {
	var h int32
	for _, u := range units {
		h = h*31 + int32(u)
	}
	return h
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

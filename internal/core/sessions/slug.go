package sessions

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// maxSlugLen is the length (in UTF-16 code units) after which Claude Code truncates a
// project folder name and appends a hash of the full path.
const maxSlugLen = 200

// Slug returns the project folder name Claude Code uses for an (already resolved)
// working directory: every UTF-16 code unit that is not [A-Za-z0-9] becomes '-', and
// names longer than 200 units are cut to 200 and suffixed with "-" plus the base-36
// absolute value of the Java String.hashCode of the full path.
//
// Pass the realpath'd directory; use ResolvedSlug to resolve symlinks first. The path
// is NFC-normalised here, as Claude Code does.
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

// ResolvedSlug resolves symlinks (falling back to the path as given, like Claude Code)
// and returns its Slug. For a directory on another machine, resolve it there and call
// Slug with the result.
func ResolvedSlug(path string) string {
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		path = rp
	}
	return Slug(path)
}

// ProjectDirName applies the CLAUDE_CODE_PROJECT_DIR_NAME override (honoured only when
// CLAUDE_CONFIG_DIR is also set and the name is valid), otherwise returns Slug(path).
func ProjectDirName(path string, getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("CLAUDE_CONFIG_DIR") != "" {
		if n := getenv("CLAUDE_CODE_PROJECT_DIR_NAME"); validPinnedName(n) {
			return n
		}
	}
	return Slug(path)
}

var pinnedNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var windowsDeviceRE = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])$`)

func validPinnedName(n string) bool {
	return pinnedNameRE.MatchString(n) && !windowsDeviceRE.MatchString(n)
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

package agent

import (
	"strconv"
	"strings"
)

// version is a parsed semantic version (https://semver.org/#spec-item-11).
type version struct {
	core [3]int
	pre  []string
}

// parseVersion reads "1.2.3", "v1.2.3", "1.2.3-alpha.1" or "1.2.3-rc.1+build": exactly
// three numeric components, optional pre-release identifiers, build metadata ignored.
func parseVersion(s string) (version, bool) {
	var v version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	s, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return v, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" || strings.Trim(id, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-") != "" {
				return v, false
			}
		}
	}
	return v, true
}

// CompareVersions orders two semantic versions by precedence: -1, 0 or 1, and ok=false
// when either does not parse. A pre-release is below its release (0.129.0-alpha <
// 0.129.0) and above every lower release (0.129.0 < 0.162.0-alpha.17.2).
func CompareVersions(a, b string) (int, bool) {
	va, oka := parseVersion(a)
	vb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range va.core {
		if va.core[i] != vb.core[i] {
			return cmpInt(va.core[i], vb.core[i]), true
		}
	}
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, true
	case len(va.pre) == 0:
		return 1, true
	case len(vb.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				return cmpInt(nx, ny), true
			}
		case ex == nil: // numeric identifiers sort below alphanumeric ones
			return -1, true
		case ey == nil:
			return 1, true
		case x != y:
			return strings.Compare(x, y), true
		}
	}
	return cmpInt(len(va.pre), len(vb.pre)), true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// VersionAtLeast reports whether version reaches minimum by semantic version precedence;
// a pre-release past the floor counts. An unknown or unparseable version fails closed.
func VersionAtLeast(version, minimum string) bool {
	c, ok := CompareVersions(version, minimum)
	return ok && c >= 0
}

// HookVersionAtLeast is VersionAtLeast within minimum's major compatibility family: a
// new major version may change the vendor's hook or launch contract, so it fails closed
// until a module verifies it.
func HookVersionAtLeast(version, minimum string) bool {
	v, ok := parseVersion(version)
	m, mok := parseVersion(minimum)
	return ok && mok && v.core[0] == m.core[0] && VersionAtLeast(version, minimum)
}

// NeedsVersion says why HookVersionAtLeast(version, minimum) failed, for people: "needs
// Codex 0.129.0 or newer; found 0.120.1".
func NeedsVersion(name, version, minimum string) string {
	need := "needs " + name + " " + minimum + " or newer"
	v, ok := parseVersion(version)
	m, _ := parseVersion(minimum)
	switch {
	case version == "":
		return need + "; its version is unknown"
	case !ok:
		return need + "; found " + version + ", which is not a version hopsesh can compare"
	case v.core[0] != m.core[0] && VersionAtLeast(version, minimum):
		return need + " in the " + strconv.Itoa(m.core[0]) + ".x family; found " + version + ", which hopsesh has not verified"
	}
	return need + "; found " + version
}

// VersionLess orders dotted versions for display and warnings: semantic version
// precedence when both parse, else numerically component by component (partial
// versions such as "0.153" included). Non-numeric suffixes in that fallback compare
// equal, as before.
func VersionLess(a, b string) bool {
	if c, ok := CompareVersions(a, b); ok {
		return c < 0
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := leadingNum(pa[i]), leadingNum(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func leadingNum(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

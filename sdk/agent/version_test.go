package agent

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.129.0", "0.129.0", 0}, {"0.130.0", "0.129.9", 1}, {"0.10.0", "0.9.0", 1}, {"v1.2.3", "1.2.3", 0},
		{"0.129.0-alpha", "0.129.0", -1}, {"0.162.0-alpha.17.2", "0.129.0", 1}, {"0.162.0-alpha.17.2", "0.162.0", -1},
		// https://semver.org/#spec-item-11 example chain
		{"1.0.0-alpha", "1.0.0-alpha.1", -1}, {"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, {"1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-beta.2", -1}, {"1.0.0-beta.2", "1.0.0-beta.11", -1}, {"1.0.0-beta.11", "1.0.0-rc.1", -1}, {"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0+a", "1.0.0+b", 0},
	} {
		got, ok := CompareVersions(c.a, c.b)
		if !ok || got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d %v, want %d", c.a, c.b, got, ok, c.want)
		}
		if back, _ := CompareVersions(c.b, c.a); back != -c.want {
			t.Errorf("CompareVersions(%s, %s) is not antisymmetric", c.b, c.a)
		}
	}
	for _, bad := range []string{"", "dev", "0.129", "0.129.0.1", "0.129.x", "0.129.0-", "0.129.0-a..b", "0.129.0-a_b", " 1 .2.3"} {
		if _, ok := CompareVersions(bad, "0.1.0"); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestVersionAtLeastAndLess(t *testing.T) {
	if !VersionAtLeast("0.162.0-alpha.17.2", "0.129.0") || VersionAtLeast("0.129.0-alpha", "0.129.0") || VersionAtLeast("", "0.1.0") || VersionAtLeast("dev", "0.1.0") {
		t.Fatal("VersionAtLeast")
	}
	if !VersionAtLeast("1.0.0", "0.129.0") || HookVersionAtLeast("1.0.0", "0.129.0") || HookVersionAtLeast("2.0.0-rc.1", "1.0.0") {
		t.Fatal("HookVersionAtLeast keeps the major compatibility family")
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.162.0-alpha.17.2", "0.162.0", true}, {"0.153", "0.153.2", true}, {"0.153.2", "0.153", false}, {"0.99.0", "0.153.2", true}, {"2.1.9", "2.1.10", true}} {
		if VersionLess(c.a, c.b) != c.want {
			t.Errorf("VersionLess(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}

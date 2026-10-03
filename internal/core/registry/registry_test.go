package registry

import "testing"

// A module's mark is shown as an image: it must be plain SVG with nothing that runs or
// reaches outside it.
func TestCheckSVG(t *testing.T) {
	ok := []string{
		"",
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><defs><linearGradient id="g"/></defs><rect fill="url(#g)" width="8" height="8"/><use href="#g"/></svg>`,
	}
	bad := []string{
		`<div/>`,
		`<svg><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"/>`,
		`<svg><image href="https://example.com/x.png"/></svg>`,
		`<svg><foreignObject/></svg>`,
		`<svg><rect`,
	}
	for _, s := range ok {
		if err := checkSVG(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range bad {
		if checkSVG(s) == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

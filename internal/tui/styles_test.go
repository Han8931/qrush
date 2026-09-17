package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// relLuminance implements the WCAG 2.1 relative-luminance formula.
func relLuminance(hex string) (float64, error) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 0, fmt.Errorf("not a 6-digit hex color: %q", hex)
	}
	var ch [3]float64
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0, err
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		ch[i] = c
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2], nil
}

func contrastRatio(t *testing.T, a, b string) float64 {
	t.Helper()
	la, err := relLuminance(a)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := relLuminance(b)
	if err != nil {
		t.Fatal(err)
	}
	hi, lo := la, lb
	if hi < lo {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// TestThemeContrast pins the readability of the palette. Every token that
// carries real text must clear WCAG AA (4.5:1) against the surface it is drawn
// on, in both light and dark mode — including the tinted cursor and selection
// rows, where a too-faint token would otherwise wash out exactly when the user
// is looking at it. Tagged rows use their dedicated selection foreground.
func TestThemeContrast(t *testing.T) {
	const (
		aa = 4.5
	)
	type surf struct {
		name            string
		panel, row, sel string
		pick            func(lipgloss.AdaptiveColor) string
	}
	// Surfaces are read from the palette itself, so retuning a fill re-runs this
	// check against the new value instead of silently drifting from it.
	darkOf := func(c lipgloss.AdaptiveColor) string { return c.Dark }
	lightOf := func(c lipgloss.AdaptiveColor) string { return c.Light }
	dark := surf{"dark", darkOf(cPanelBg), darkOf(cRowFocusBg), darkOf(cSelectBg), darkOf}
	light := surf{"light", lightOf(cPanelBg), lightOf(cRowFocusBg), lightOf(cSelectBg), lightOf}

	text := map[string]lipgloss.AdaptiveColor{
		"cFgBright": cFgBright, "cFg": cFg, "cFgMuted": cFgMuted, "cFgFaint": cFgFaint,
		"cAccent": cAccent, "cGreen": cGreen, "cAmber": cAmber,
		"cRed": cRed, "cBlue": cBlue, "cFgDim": cFgDim,
	}

	for _, s := range []surf{dark, light} {
		for name, tok := range text {
			hex := s.pick(tok)
			if got := contrastRatio(t, hex, s.panel); got < aa {
				t.Errorf("%s/%s (%s) vs panel: %.2f:1, want >= %.1f", s.name, name, hex, got, aa)
			}
			for label, bg := range map[string]string{"cursor row": s.row} {
				if got := contrastRatio(t, hex, bg); got < aa {
					t.Errorf("%s/%s (%s) vs %s: %.2f:1, want >= %.1f", s.name, name, hex, label, got, aa)
				}
			}
		}
		for name, pair := range map[string][2]lipgloss.AdaptiveColor{
			"selection":       {cSelectFg, cSelectBg},
			"inactive cursor": {cInactFg, cInactBg},
			"header":          {cFgBright, cHeaderTint},
			"muted header":    {cFgMuted, cHeaderTint},
			"footer":          {cFgMuted, cBarBg},
			"focused footer":  {cFgBright, cSubtleBg},
			"normal badge":    {cInk, cAccent},
			"insert badge":    {cInk, cGreen},
			"command badge":   {cInk, cAmber},
			"error badge":     {cInk, cRed},
		} {
			if got := contrastRatio(t, s.pick(pair[0]), s.pick(pair[1])); got < aa {
				t.Errorf("%s/%s: %.2f:1, want >= %.1f", s.name, name, got, aa)
			}
		}
	}
}

// The cursor and tagged fills must be distinguishable from the panel and from
// each other, or the two selection states collapse into one look.
func TestSelectionSurfacesDiffer(t *testing.T) {
	for _, tc := range []struct{ name, panel, row, sel string }{
		{"dark", cPanelBg.Dark, cRowFocusBg.Dark, cSelectBg.Dark},
		{"light", cPanelBg.Light, cRowFocusBg.Light, cSelectBg.Light},
	} {
		if r := contrastRatio(t, tc.row, tc.panel); r < 1.15 {
			t.Errorf("%s: cursor row vs panel only %.3f:1 — cursor is invisible", tc.name, r)
		}
		if r := contrastRatio(t, tc.sel, tc.panel); r < 1.05 {
			t.Errorf("%s: tagged row vs panel only %.3f:1 — tag tint is invisible", tc.name, r)
		}
		if r := contrastRatio(t, tc.row, tc.sel); r < 1.05 {
			t.Errorf("%s: cursor vs tagged only %.3f:1 — the two states look identical", tc.name, r)
		}
	}
}

package tui

import (
	"bytes"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestColorSGR(t *testing.T) {
	cases := []struct {
		c    vt10x.Color
		base string
		want string
	}{
		{vt10x.Black, "38", "38;5;0"},         // palette 0 is a real color, not "unset"
		{vt10x.Red, "38", "38;5;1"},           // ANSI colors are 0-based — no off-by-one
		{vt10x.Color(203), "48", "48;5;203"},  // xterm palette
		{vt10x.Color(0x123456), "38", "38;2;18;52;86"}, // packed truecolor
		{vt10x.DefaultFG, "38", ""},           // sentinels mean "terminal default"
		{vt10x.DefaultBG, "48", ""},
	}
	for _, tc := range cases {
		if got := colorSGR(tc.c, tc.base); got != tc.want {
			t.Errorf("colorSGR(%d, %s) = %q, want %q", tc.c, tc.base, got, tc.want)
		}
	}
}

func TestSGRPrefix(t *testing.T) {
	if got := (termStyle{cursor: true}).sgrPrefix(); got != "\x1b[7m" {
		t.Errorf("cursor prefix = %q", got)
	}
	if got := (termStyle{fg: vt10x.DefaultFG, bg: vt10x.DefaultBG}).sgrPrefix(); got != "" {
		t.Errorf("default style should need no prefix, got %q", got)
	}
	if got := (termStyle{fg: vt10x.Green, bg: vt10x.DefaultBG}).sgrPrefix(); got != "\x1b[38;5;2m" {
		t.Errorf("green fg prefix = %q", got)
	}
	if got := (termStyle{fg: vt10x.Green, bg: vt10x.Color(17)}).sgrPrefix(); got != "\x1b[38;5;2;48;5;17m" {
		t.Errorf("fg+bg prefix = %q", got)
	}
}

func TestClearAfterAltScreenExit(t *testing.T) {
	in := []byte("before\x1b[?1049lprompt")
	out, clear := clearAfterAltScreenExit(in)
	if !clear {
		t.Fatal("expected clear flag")
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("expected unchanged output, got %q", out)
	}
}

func TestClearAfterAltScreenExitUnchanged(t *testing.T) {
	in := []byte("plain output")
	out, clear := clearAfterAltScreenExit(in)
	if clear {
		t.Fatal("did not expect clear flag")
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("expected unchanged output, got %q", out)
	}
}

func TestNormalizePromptMarkerStartsFreshLine(t *testing.T) {
	sh := &shellState{vt: vt10x.New(vt10x.WithSize(20, 5))}
	sh.vt.Write([]byte("logo"))

	out, fixed := sh.normalizePromptMarker([]byte("\x1b]133;A\aprompt"))
	if !fixed {
		t.Fatal("expected prompt marker to be fixed")
	}
	if !bytes.Equal(out, []byte("\x1b[2;1H\x1b[Kprompt")) {
		t.Fatalf("expected prompt after leftover content, got %q", out)
	}
}

func TestNormalizePromptMarkerAtLineStart(t *testing.T) {
	sh := &shellState{vt: vt10x.New(vt10x.WithSize(20, 5))}

	out, fixed := sh.normalizePromptMarker([]byte("\x1b]133;A\aprompt"))
	if fixed {
		t.Fatal("did not expect prompt marker to need a new line")
	}
	if !bytes.Equal(out, []byte("prompt")) {
		t.Fatalf("expected marker stripped, got %q", out)
	}
	if !sh.promptReady() {
		t.Fatal("expected shell prompt to be marked ready")
	}
	sh.write([]byte("x"))
	if sh.promptReady() {
		t.Fatal("expected shell prompt marker to clear after input")
	}
}

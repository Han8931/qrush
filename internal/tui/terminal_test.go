package tui

import (
	"bytes"
	"testing"

	"github.com/hinshun/vt10x"
)

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

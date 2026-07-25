package vt10x

import (
	"io"
	"testing"
)

// A double-width glyph occupies two columns: the rune in the leading cell, a
// wide-dummy placeholder in the trailing cell, and the next rune two columns on.
func TestWideCharLayout(t *testing.T) {
	term := New()
	if _, err := term.Write([]byte("📁x")); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if got := term.Cell(0, 0).Char; got != '📁' {
		t.Fatalf("cell 0 = %q, want the wide rune", string(got))
	}
	if !term.Cell(1, 0).WideDummy() {
		t.Fatalf("cell 1 should be a wide-dummy placeholder")
	}
	if got := term.Cell(2, 0).Char; got != 'x' {
		t.Fatalf("cell 2 = %q, want 'x' (two columns after the wide glyph)", string(got))
	}
}

// A plain ASCII run keeps one column per rune and never marks a dummy.
func TestNarrowCharsNoDummy(t *testing.T) {
	term := New()
	if _, err := term.Write([]byte("abc")); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	for i, want := range []rune{'a', 'b', 'c'} {
		if got := term.Cell(i, 0).Char; got != want {
			t.Fatalf("cell %d = %q, want %q", i, string(got), string(want))
		}
		if term.Cell(i, 0).WideDummy() {
			t.Fatalf("cell %d should not be a wide-dummy", i)
		}
	}
}

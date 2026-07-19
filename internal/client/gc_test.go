package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSweepOrphans(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	orphan := write("ru_1_deadbeef.out")
	orphanErr := write("ru_1_deadbeef.out.e")
	kept := write("ru_2_0badcafe.out")
	fallback := write("ru_3_1721380000000.out") // decimal fallback suffix
	custom := write("custom.log")               // user-named, never a candidate
	unrelated := write("ru_notes.out")          // doesn't match the pattern

	refs := map[string]bool{kept: true, kept + ".e": true}
	n, freed, err := SweepOrphans(dir, refs)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || freed != 3 {
		t.Errorf("expected 3 files / 3 bytes swept, got %d / %d", n, freed)
	}

	for _, gone := range []string{orphan, orphanErr, fallback} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s to be swept", gone)
		}
	}
	for _, stay := range []string{kept, custom, unrelated} {
		if _, err := os.Stat(stay); err != nil {
			t.Errorf("expected %s to survive: %v", stay, err)
		}
	}
}

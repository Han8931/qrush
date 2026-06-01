package server

import (
	"bytes"
	"testing"
	"time"
)

func TestTerminalManagerReattachReplaysBacklog(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewTerminalManager()
	term, err := m.GetOrCreate("test", "main", 80, 24)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	defer m.Kill("test", "main")

	ch, _ := term.Attach()
	term.Write([]byte("printf qrush-attach-ok\\n\r"))
	if !readUntil(t, ch, []byte("qrush-attach-ok"), 2*time.Second) {
		t.Fatal("did not see command output")
	}
	term.Detach(ch)

	_, backlog := term.Attach()
	if !bytes.Contains(backlog, []byte("qrush-attach-ok")) {
		t.Fatalf("expected backlog to contain command output, got %q", backlog)
	}
}

func TestTerminalOpenAssignsUniqueNames(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewTerminalManager()
	a, err := m.Open("s", 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	b, err := m.Open("s", 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.KillAll()
	if a == b {
		t.Fatalf("expected unique pane names, got %q twice", a)
	}
}

func TestTerminalLayoutPersistAndReap(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewTerminalManager()
	a, _ := m.Open("s", 80, 24)
	b, _ := m.Open("s", 80, 24)
	defer m.KillAll()

	blob := []byte("layout-blob")
	// Keep only pane a; b must be reaped.
	m.SetLayout("s", blob, []string{a})

	gotBlob, alive := m.ListLayout("s")
	if string(gotBlob) != "layout-blob" {
		t.Fatalf("layout blob = %q, want layout-blob", gotBlob)
	}
	if len(alive) != 1 || alive[0] != a {
		t.Fatalf("alive = %v, want [%s]", alive, a)
	}
	if _, ok := m.sessions[terminalKey("s", b)]; ok {
		t.Fatalf("pane %s should have been reaped", b)
	}
}

func TestTerminalListAll(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := NewTerminalManager()
	a, _ := m.Open("s1", 80, 24)
	b, _ := m.Open("s2", 80, 24)
	defer m.KillAll()

	all := m.ListAll()
	if len(all) != 2 {
		t.Fatalf("ListAll = %d entries, want 2", len(all))
	}
	seen := map[string]bool{}
	for _, ti := range all {
		seen[ti.Session+"/"+ti.Pane] = true
	}
	if !seen["s1/"+a] || !seen["s2/"+b] {
		t.Fatalf("ListAll missing expected panes: %+v", all)
	}
}

func readUntil(t *testing.T, ch <-chan []byte, needle []byte, timeout time.Duration) bool {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var buf []byte
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return false
			}
			buf = append(buf, data...)
			if bytes.Contains(buf, needle) {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

package server

import (
	"strings"
	"testing"
)

func TestEnvStoreSetGet(t *testing.T) {
	e := NewEnvStore()
	e.Set("FOO", "bar")

	v, ok := e.Get("FOO")
	if !ok || v != "bar" {
		t.Errorf("expected 'bar', got %q (ok=%v)", v, ok)
	}
}

func TestEnvStoreApplyOverridesAndAdds(t *testing.T) {
	store := NewEnvStore()
	store.Set("FOO", "server")
	store.Set("BAZ", "added")

	got := store.Apply([]string{"FOO=client", "BAR=keep"})

	values := make(map[string]string)
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}

	if values["FOO"] != "server" {
		t.Fatalf("expected FOO override, got %q", values["FOO"])
	}
	if values["BAR"] != "keep" {
		t.Fatalf("expected BAR to remain, got %q", values["BAR"])
	}
	if values["BAZ"] != "added" {
		t.Fatalf("expected BAZ to be added, got %q", values["BAZ"])
	}
}

func TestEnvStoreGetMissing(t *testing.T) {
	e := NewEnvStore()
	_, ok := e.Get("MISSING")
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestEnvStoreUnset(t *testing.T) {
	e := NewEnvStore()
	e.Set("FOO", "bar")
	e.Unset("FOO")

	_, ok := e.Get("FOO")
	if ok {
		t.Error("expected ok=false after unset")
	}
}

func TestEnvStoreOverwrite(t *testing.T) {
	e := NewEnvStore()
	e.Set("KEY", "v1")
	e.Set("KEY", "v2")

	v, _ := e.Get("KEY")
	if v != "v2" {
		t.Errorf("expected 'v2', got %q", v)
	}
}

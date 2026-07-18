package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the config and state paths at temp dirs and clears every
// qrush-related environment variable, so tests never see the real user config.
func isolate(t *testing.T) (cfgDir, stateDir string) {
	t.Helper()
	cfgHome, stateHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	for _, k := range Keys {
		t.Setenv(k.EnvVar, "")
		t.Setenv(k.LegacyEnv, "")
	}
	return filepath.Join(cfgHome, "qrush"), filepath.Join(stateHome, "qrush")
}

func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	isolate(t)
	c := Load()

	if c.MaxFinished != -1 {
		t.Errorf("expected MaxFinished=-1, got %d", c.MaxFinished)
	}
	if c.MaxConn != 10 {
		t.Errorf("expected MaxConn=10, got %d", c.MaxConn)
	}
	if c.Slots != 1 {
		t.Errorf("expected Slots=1, got %d", c.Slots)
	}
	if c.Socket != "" {
		t.Errorf("expected empty Socket, got %q", c.Socket)
	}
}

func TestLoadFromEnv(t *testing.T) {
	isolate(t)
	t.Setenv("QRUSH_SOCKET", "/tmp/test.sock")
	t.Setenv("QRUSH_MAXFINISHED", "50")
	t.Setenv("QRUSH_MAXCONN", "20")
	t.Setenv("QRUSH_SLOTS", "4")
	t.Setenv("QRUSH_ONFINISH", "notify-send done")
	t.Setenv("QRUSH_SAVELIST", "/tmp/savelist")

	c := Load()

	if c.Socket != "/tmp/test.sock" {
		t.Errorf("expected Socket='/tmp/test.sock', got %q", c.Socket)
	}
	if c.MaxFinished != 50 {
		t.Errorf("expected MaxFinished=50, got %d", c.MaxFinished)
	}
	if c.MaxConn != 20 {
		t.Errorf("expected MaxConn=20, got %d", c.MaxConn)
	}
	if c.Slots != 4 {
		t.Errorf("expected Slots=4, got %d", c.Slots)
	}
	if c.OnFinish != "notify-send done" {
		t.Errorf("expected OnFinish='notify-send done', got %q", c.OnFinish)
	}
	if c.SaveList != "/tmp/savelist" {
		t.Errorf("expected SaveList='/tmp/savelist', got %q", c.SaveList)
	}
}

func TestLoadInvalidNumbersWarn(t *testing.T) {
	isolate(t)
	t.Setenv("QRUSH_MAXFINISHED", "abc")
	t.Setenv("QRUSH_MAXCONN", "xyz")
	t.Setenv("QRUSH_SLOTS", "bad")

	c, _, warnings := LoadDetailed()

	if c.MaxFinished != -1 {
		t.Errorf("expected MaxFinished=-1 on invalid, got %d", c.MaxFinished)
	}
	if c.MaxConn != 10 {
		t.Errorf("expected MaxConn=10 on invalid, got %d", c.MaxConn)
	}
	if c.Slots != 1 {
		t.Errorf("expected Slots=1 on invalid, got %d", c.Slots)
	}
	if len(warnings) != 3 {
		t.Errorf("expected 3 warnings for 3 bad values, got %v", warnings)
	}
}

func TestLoadZeroSlotsClamped(t *testing.T) {
	isolate(t)
	t.Setenv("QRUSH_SLOTS", "0")
	if c := Load(); c.Slots != 1 {
		t.Errorf("expected Slots=1 for zero input, got %d", c.Slots)
	}
}

func TestLoadLegacyEnvFallback(t *testing.T) {
	isolate(t)
	t.Setenv("TS_SOCKET", "/tmp/legacy.sock")
	t.Setenv("TS_SLOTS", "3")

	c := Load()

	if c.Socket != "/tmp/legacy.sock" {
		t.Errorf("expected legacy Socket='/tmp/legacy.sock', got %q", c.Socket)
	}
	if c.Slots != 3 {
		t.Errorf("expected legacy Slots=3, got %d", c.Slots)
	}
}

func TestLayerPrecedence(t *testing.T) {
	cfgDir, _ := isolate(t)
	writeConfigFile(t, cfgDir, "slots = 3\nlogdir = /file/logs\n")

	// File beats default.
	c, settings, _ := LoadDetailed()
	if c.Slots != 3 {
		t.Fatalf("file layer: expected slots 3, got %d", c.Slots)
	}
	for _, s := range settings {
		if s.Key.Name == "slots" && s.Source != SourceFile {
			t.Errorf("expected slots source=file, got %s", s.Source)
		}
	}

	// Env beats file.
	t.Setenv("QRUSH_SLOTS", "5")
	c, settings, _ = LoadDetailed()
	if c.Slots != 5 {
		t.Fatalf("env layer: expected slots 5, got %d", c.Slots)
	}

	// Runtime beats env.
	if err := SaveRuntime("slots", "7"); err != nil {
		t.Fatal(err)
	}
	c, settings, _ = LoadDetailed()
	if c.Slots != 7 {
		t.Fatalf("runtime layer: expected slots 7, got %d", c.Slots)
	}
	for _, s := range settings {
		if s.Key.Name == "slots" && s.Source != SourceRuntime {
			t.Errorf("expected slots source=runtime, got %s", s.Source)
		}
	}

	// Deleting the runtime override falls back to env.
	if err := DeleteRuntime("slots"); err != nil {
		t.Fatal(err)
	}
	if c := Load(); c.Slots != 5 {
		t.Errorf("after DeleteRuntime: expected slots 5, got %d", c.Slots)
	}
}

func TestSetFileValuePreservesFile(t *testing.T) {
	cfgDir, _ := isolate(t)
	writeConfigFile(t, cfgDir, "# my settings\nslots = 2\non_finish = echo hi\n")

	if err := SetFileValue("slots", "6"); err != nil {
		t.Fatal(err)
	}
	if err := SetFileValue("max_conn", "15"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# my settings") {
		t.Error("comment was not preserved")
	}
	if !strings.Contains(text, "slots = 6") || strings.Contains(text, "slots = 2") {
		t.Errorf("slots line not replaced: %q", text)
	}
	if !strings.Contains(text, "on_finish = echo hi") {
		t.Error("unrelated key was not preserved")
	}
	if !strings.Contains(text, "max_conn = 15") {
		t.Error("new key was not appended")
	}
}

func TestFileTildeExpansion(t *testing.T) {
	cfgDir, _ := isolate(t)
	writeConfigFile(t, cfgDir, "logdir = ~/qlogs\n")
	home, _ := os.UserHomeDir()
	if c := Load(); c.Logdir != filepath.Join(home, "qlogs") {
		t.Errorf("expected tilde-expanded logdir, got %q", c.Logdir)
	}
}

func TestUnknownFileKeyWarns(t *testing.T) {
	cfgDir, _ := isolate(t)
	writeConfigFile(t, cfgDir, "bogus = 1\n")
	_, _, warnings := LoadDetailed()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus") {
		t.Errorf("expected unknown-key warning, got %v", warnings)
	}
}

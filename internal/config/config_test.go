package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	os.Unsetenv("QRUSH_SOCKET")
	os.Unsetenv("QRUSH_MAILTO")
	os.Unsetenv("QRUSH_MAXFINISHED")
	os.Unsetenv("QRUSH_MAXCONN")
	os.Unsetenv("QRUSH_SLOTS")
	os.Unsetenv("QRUSH_ONFINISH")
	os.Unsetenv("QRUSH_SAVELIST")
	os.Unsetenv("TS_SOCKET")
	os.Unsetenv("TS_MAILTO")
	os.Unsetenv("TS_MAXFINISHED")
	os.Unsetenv("TS_MAXCONN")
	os.Unsetenv("TS_SLOTS")
	os.Unsetenv("TS_ONFINISH")
	os.Unsetenv("TS_SAVELIST")

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
	t.Setenv("QRUSH_SOCKET", "/tmp/test.sock")
	t.Setenv("QRUSH_MAILTO", "user@example.com")
	t.Setenv("QRUSH_MAXFINISHED", "50")
	t.Setenv("QRUSH_MAXCONN", "20")
	t.Setenv("QRUSH_SLOTS", "4")
	t.Setenv("QRUSH_ONFINISH", "notify-send done")
	t.Setenv("QRUSH_SAVELIST", "/tmp/savelist")

	c := Load()

	if c.Socket != "/tmp/test.sock" {
		t.Errorf("expected Socket='/tmp/test.sock', got %q", c.Socket)
	}
	if c.MailTo != "user@example.com" {
		t.Errorf("expected MailTo='user@example.com', got %q", c.MailTo)
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

func TestLoadInvalidNumbers(t *testing.T) {
	t.Setenv("QRUSH_MAXFINISHED", "abc")
	t.Setenv("QRUSH_MAXCONN", "xyz")
	t.Setenv("QRUSH_SLOTS", "bad")

	c := Load()

	if c.MaxFinished != -1 {
		t.Errorf("expected MaxFinished=-1 on invalid, got %d", c.MaxFinished)
	}
	if c.MaxConn != 10 {
		t.Errorf("expected MaxConn=10 on invalid, got %d", c.MaxConn)
	}
	if c.Slots != 1 {
		t.Errorf("expected Slots=1 on invalid, got %d", c.Slots)
	}
}

func TestLoadZeroSlots(t *testing.T) {
	t.Setenv("QRUSH_SLOTS", "0")
	c := Load()
	if c.Slots != 1 {
		t.Errorf("expected Slots=1 for zero input, got %d", c.Slots)
	}
}

func TestLoadLegacyEnvFallback(t *testing.T) {
	t.Setenv("QRUSH_SOCKET", "")
	t.Setenv("QRUSH_SLOTS", "")
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

package config

import (
	"os"
	"strconv"
)

type Config struct {
	Socket      string
	MailTo      string
	MaxFinished int
	MaxConn     int
	OnFinish    string
	Env         string
	SaveList    string
	Slots       int
	TmpDir      string
}

func Load() *Config {
	c := &Config{
		MaxFinished: -1,
		MaxConn:     10,
		Slots:       1,
	}

	c.Socket = getenv("QRUSH_SOCKET", "TS_SOCKET")
	c.MailTo = getenv("QRUSH_MAILTO", "TS_MAILTO")
	c.OnFinish = getenv("QRUSH_ONFINISH", "TS_ONFINISH")
	c.Env = getenv("QRUSH_ENV", "TS_ENV")
	c.SaveList = getenv("QRUSH_SAVELIST", "TS_SAVELIST")

	if v := getenv("QRUSH_MAXFINISHED", "TS_MAXFINISHED"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxFinished = n
		}
	}
	if v := getenv("QRUSH_MAXCONN", "TS_MAXCONN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxConn = n
		}
	}
	if v := getenv("QRUSH_SLOTS", "TS_SLOTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Slots = n
		}
	}

	c.TmpDir = os.Getenv("TMPDIR")
	if c.TmpDir == "" {
		c.TmpDir = os.TempDir()
	}

	return c
}

func getenv(name, legacyName string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return os.Getenv(legacyName)
}

package config

import (
	"os"
	"strconv"
)

type Config struct {
	Socket      string
	MaxFinished int
	MaxConn     int
	OnFinish    string
	SaveList    string
	Slots       int
	Logdir      string // job output directory; empty means TmpDir
	TmpDir      string
}

// Load resolves the effective configuration (defaults < file < env < runtime).
func Load() *Config {
	c, _, _ := LoadDetailed()
	return c
}

// LoadDetailed additionally returns each setting with its source layer, plus
// warnings for values that were rejected (e.g. non-numeric slots).
func LoadDetailed() (*Config, []Setting, []string) {
	settings, warnings := loadSettings()
	get := func(name string) string {
		for _, s := range settings {
			if s.Key.Name == name {
				return s.Value
			}
		}
		return ""
	}
	atoi := func(v string, fallback int) int {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return fallback
	}

	c := &Config{
		Socket:      get("socket"),
		MaxFinished: atoi(get("max_finished"), -1),
		MaxConn:     atoi(get("max_conn"), 10),
		OnFinish:    get("on_finish"),
		SaveList:    get("save_list"),
		Slots:       atoi(get("slots"), 1),
		Logdir:      get("logdir"),
	}

	c.TmpDir = os.Getenv("TMPDIR")
	if c.TmpDir == "" {
		c.TmpDir = os.TempDir()
	}
	return c, settings, warnings
}

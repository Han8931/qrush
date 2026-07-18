package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// qrush settings are layered; later layers win:
//
//	built-in defaults < config file < environment < runtime state
//
// The config file is ~/.config/qrush/config (XDG_CONFIG_HOME respected),
// plain `key = value` lines. The runtime state file records settings changed
// while the daemon runs (-P, --set_logdir, the TUI's :config box) so they
// survive daemon restarts; it outranks the environment because it records the
// user's most recent explicit choice.

// Source names the layer a setting's effective value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceRuntime Source = "runtime"
)

// Key describes one configurable setting.
type Key struct {
	Name      string // config-file key
	EnvVar    string
	LegacyEnv string
	Default   string
	Desc      string
	IsInt     bool
	MinInt    int  // clamp floor, only when IsInt
	IsPath    bool // expand a leading ~/
	Runtime   bool // may be overridden by the runtime state file
}

// Keys lists every setting, in display order.
var Keys = []Key{
	{Name: "slots", EnvVar: "QRUSH_SLOTS", LegacyEnv: "TS_SLOTS", Default: "1",
		Desc: "max simultaneous job slots", IsInt: true, MinInt: 1, Runtime: true},
	{Name: "logdir", EnvVar: "QRUSH_LOGDIR", LegacyEnv: "TS_LOGDIR", Default: "",
		Desc: "directory for job output files (empty: $TMPDIR)", IsPath: true, Runtime: true},
	{Name: "socket", EnvVar: "QRUSH_SOCKET", LegacyEnv: "TS_SOCKET", Default: "",
		Desc: "daemon socket path (empty: auto per-user path)", IsPath: true},
	{Name: "max_finished", EnvVar: "QRUSH_MAXFINISHED", LegacyEnv: "TS_MAXFINISHED", Default: "-1",
		Desc: "finished jobs to keep (-1: unlimited)", IsInt: true, MinInt: -1},
	{Name: "max_conn", EnvVar: "QRUSH_MAXCONN", LegacyEnv: "TS_MAXCONN", Default: "10",
		Desc: "max simultaneous client connections", IsInt: true, MinInt: 1},
	{Name: "on_finish", EnvVar: "QRUSH_ONFINISH", LegacyEnv: "TS_ONFINISH", Default: "",
		Desc: "command run after each job finishes"},
	{Name: "save_list", EnvVar: "QRUSH_SAVELIST", LegacyEnv: "TS_SAVELIST", Default: "",
		Desc: "file persisting the job queue across restarts", IsPath: true},
}

// KeyByName returns the Key metadata for a config name.
func KeyByName(name string) (Key, bool) {
	for _, k := range Keys {
		if k.Name == name {
			return k, true
		}
	}
	return Key{}, false
}

// Setting is one key's effective value and the layer it came from.
type Setting struct {
	Key    Key
	Value  string
	Source Source
}

// ConfigDir returns the directory holding the config file.
func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "qrush")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "qrush")
}

// FilePath returns the config file's path.
func FilePath() string {
	d := ConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config")
}

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "qrush")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "qrush")
}

// RuntimeStatePath returns the runtime-overrides file's path.
func RuntimeStatePath() string {
	d := stateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "runtime")
}

// parseKV parses `key = value` lines; '#' starts a comment, blanks skipped.
// Unknown keys are returned so callers can warn rather than silently ignore.
func parseKV(data string) (map[string]string, []string) {
	out := make(map[string]string)
	var unknown []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			unknown = append(unknown, line)
			continue
		}
		name := strings.ToLower(strings.TrimSpace(k))
		if _, known := KeyByName(name); !known {
			unknown = append(unknown, name)
			continue
		}
		out[name] = strings.TrimSpace(v)
	}
	return out, unknown
}

func readKVFile(path string) (map[string]string, []string) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	return parseKV(string(data))
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// loadSettings resolves every key through the layers, returning the effective
// settings (in Keys order) plus human-readable warnings for rejected values.
func loadSettings() ([]Setting, []string) {
	var warnings []string
	fileVals, unknown := readKVFile(FilePath())
	for _, u := range unknown {
		warnings = append(warnings, fmt.Sprintf("config file: unknown key %q", u))
	}
	runtimeVals, _ := readKVFile(RuntimeStatePath())

	settings := make([]Setting, 0, len(Keys))
	for _, k := range Keys {
		s := Setting{Key: k, Value: k.Default, Source: SourceDefault}
		apply := func(raw string, src Source, origin string) {
			if k.IsInt {
				n, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("%s: invalid %s %q (keeping %s)", origin, k.Name, raw, s.Value))
					return
				}
				if n < k.MinInt {
					n = k.MinInt
				}
				raw = strconv.Itoa(n)
			}
			if k.IsPath {
				raw = expandTilde(raw)
			}
			s.Value, s.Source = raw, src
		}

		if v, ok := fileVals[k.Name]; ok {
			apply(v, SourceFile, "config file")
		}
		if v := os.Getenv(k.EnvVar); v != "" {
			apply(v, SourceEnv, "$"+k.EnvVar)
		} else if v := os.Getenv(k.LegacyEnv); v != "" {
			apply(v, SourceEnv, "$"+k.LegacyEnv)
		}
		if k.Runtime {
			if v, ok := runtimeVals[k.Name]; ok {
				apply(v, SourceRuntime, "runtime state")
			}
		}
		settings = append(settings, s)
	}
	return settings, warnings
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".qrush-cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// SaveRuntime records a runtime override (daemon-side: -P, --set_logdir, the
// TUI's :config box) so it survives daemon restarts.
func SaveRuntime(key, value string) error {
	path := RuntimeStatePath()
	if path == "" {
		return fmt.Errorf("no home directory for runtime state")
	}
	vals, _ := readKVFile(path)
	if vals == nil {
		vals = make(map[string]string)
	}
	vals[key] = value
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# qrush runtime overrides — written by the daemon; edit via `ru config`.\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s = %s\n", n, vals[n])
	}
	return writeFileAtomic(path, []byte(b.String()))
}

// DeleteRuntime drops a runtime override, letting the file/env layers win
// again. Missing files or keys are fine.
func DeleteRuntime(key string) error {
	path := RuntimeStatePath()
	if path == "" {
		return nil
	}
	vals, _ := readKVFile(path)
	if _, ok := vals[key]; !ok {
		return nil
	}
	delete(vals, key)
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# qrush runtime overrides — written by the daemon; edit via `ru config`.\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s = %s\n", n, vals[n])
	}
	return writeFileAtomic(path, []byte(b.String()))
}

// SetFileValue writes key = value into the config file, replacing the key's
// existing line but otherwise preserving the file (comments included).
func SetFileValue(key, value string) error {
	path := FilePath()
	if path == "" {
		return fmt.Errorf("no home directory for config file")
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	newLine := fmt.Sprintf("%s = %s", key, value)
	replaced := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && strings.ToLower(strings.TrimSpace(k)) == key {
			lines[i] = newLine
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}
	out := strings.Join(lines, "\n")
	out = strings.TrimPrefix(out, "\n") + "\n"
	return writeFileAtomic(path, []byte(out))
}

// FileTemplate is written by `ru config edit` when no config file exists yet.
func FileTemplate() string {
	var b strings.Builder
	b.WriteString(`# qrush configuration — key = value, one per line; '#' starts a comment.
# Precedence: runtime overrides (settings changed live) > environment
# variables > this file > built-in defaults.
# Run 'ru config list' to see every key's effective value and source.

`)
	for _, k := range Keys {
		def := k.Default
		if def == "" {
			def = "..."
		}
		fmt.Fprintf(&b, "# %s = %s\t# %s\n", k.Name, def, k.Desc)
	}
	return b.String()
}

package client

import (
	"os"
	"path/filepath"
	"regexp"
)

// orphanPattern matches qrush's generated output filenames (ru_<id>_<rand>.out
// plus their .e stderr companions). The random part is hex, or decimal when
// the random source was unavailable. User-named -O files don't match.
var orphanPattern = regexp.MustCompile(`^ru_\d+_[0-9a-f]+\.out(\.e)?$`)

// SweepOrphans deletes generated job-output files in dir that no live job
// references, returning the number of files removed and the bytes freed.
// Only names matching the generated pattern are candidates, so it never
// touches foreign files even in a shared directory like $TMPDIR.
func SweepOrphans(dir string, referenced map[string]bool) (int, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var freed int64
	for _, e := range entries {
		if e.IsDir() || !orphanPattern.MatchString(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if referenced[full] {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		if os.Remove(full) == nil {
			count++
			freed += size
		}
	}
	return count, freed, nil
}

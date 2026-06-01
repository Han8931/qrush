//go:build darwin

package sysmon

import (
	"bufio"
	"encoding/binary"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func (m *Monitor) sample(s *Stats) {
	sampleLoad(s)
	sampleMem(s)
	// macOS exposes accurate per-core CPU ticks only through the mach host APIs,
	// which need cgo. To stay dependency- and cgo-free we approximate "how busy
	// is the machine" from the 1-minute load average over the core count — a
	// standard, if coarse, saturation proxy.
	if s.LoadOK && s.NumCPU > 0 {
		s.CPUPercent = clampPercent(s.Load[0] / float64(s.NumCPU) * 100)
		s.CPUOK = true
	}
}

// sampleLoad parses the vm.loadavg sysctl, which returns a C
// `struct loadavg { fixpt_t ldavg[3]; long fscale; }`.
func sampleLoad(s *Stats) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 12 {
		return
	}
	scale := 2048.0 // classic fixed-point scale; overridden by fscale when present
	if len(raw) >= 24 {
		if f := int64(binary.LittleEndian.Uint64(raw[16:24])); f != 0 {
			scale = float64(f)
		}
	}
	for i := 0; i < 3; i++ {
		v := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
		s.Load[i] = float64(v) / scale
	}
	s.LoadOK = true
}

func sampleMem(s *Stats) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		return
	}
	s.MemTotal = total
	s.MemOK = true
	if used, ok := vmStatUsed(); ok && used <= total {
		s.MemUsed = used
	}
}

// vmStatUsed approximates in-use physical memory (active + wired + compressed)
// by parsing vm_stat(1) output. This mirrors how Activity Monitor reports
// "memory used" and excludes reclaimable inactive/cached pages.
func vmStatUsed() (uint64, bool) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, false
	}
	pageSize := uint64(4096)
	var active, wired, compressed uint64
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "page size of") {
			for _, tok := range strings.Fields(line) {
				if v, err := strconv.ParseUint(tok, 10, 64); err == nil {
					pageSize = v
					break
				}
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "."))
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			continue
		}
		switch {
		case key == "Pages active":
			active = n
		case key == "Pages wired down":
			wired = n
		case strings.HasPrefix(key, "Pages occupied by compressor"):
			compressed = n
		}
	}
	return (active + wired + compressed) * pageSize, true
}

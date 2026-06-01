//go:build linux

package sysmon

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func (m *Monitor) sample(s *Stats) {
	m.sampleCPU(s)
	sampleMem(s)
	sampleLoad(s)
}

// sampleCPU reads aggregate CPU ticks from /proc/stat and reports the busy
// fraction since the previous sample.
func (m *Monitor) sampleCPU(s *Stats) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return
	}
	fields := strings.Fields(sc.Text())
	// "cpu user nice system idle iowait irq softirq steal guest guest_nice"
	if len(fields) < 5 || fields[0] != "cpu" {
		return
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 4 || i == 5 { // idle + iowait count as not-busy
			idle += v
		}
	}
	busy := total - idle

	if m.havePrev && total > m.prevTotal {
		dt := total - m.prevTotal
		db := busy - m.prevBusy
		s.CPUPercent = clampPercent(float64(db) / float64(dt) * 100)
		s.CPUOK = true
	}
	m.prevBusy, m.prevTotal, m.havePrev = busy, total, true
}

func sampleMem(s *Stats) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				total, haveTotal = v*1024, true
			}
		case "MemAvailable:":
			if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				avail, haveAvail = v*1024, true
			}
		}
		if haveTotal && haveAvail {
			break
		}
	}
	if haveTotal {
		s.MemTotal = total
		if haveAvail && avail <= total {
			s.MemUsed = total - avail
		}
		s.MemOK = true
	}
}

func sampleLoad(s *Stats) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return
		}
		s.Load[i] = v
	}
	s.LoadOK = true
}

// Package sysmon collects coarse, system-wide hardware metrics (CPU, memory,
// load average) with no external dependencies. Collection is platform-specific
// and degrades gracefully: each metric carries an "OK" flag, and unsupported
// platforms simply report nothing available.
package sysmon

import "runtime"

// Stats is a single snapshot of system-wide resource usage.
type Stats struct {
	CPUPercent float64 // 0..100, busy across all cores
	CPUOK      bool

	MemUsed  uint64 // bytes
	MemTotal uint64 // bytes
	MemOK    bool

	Load   [3]float64 // 1, 5, 15 minute load averages
	LoadOK bool

	NumCPU int
}

// Monitor samples Stats over time. CPU percentage needs the delta between two
// samples, so a Monitor retains the previous reading; create one with New and
// reuse it. A Monitor is not safe for concurrent use.
type Monitor struct {
	prevBusy  uint64
	prevTotal uint64
	havePrev  bool
}

// New returns a ready-to-use Monitor.
func New() *Monitor { return &Monitor{} }

func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// Sample collects a fresh snapshot. The first CPU sample on a fresh Monitor has
// no prior reading to diff against, so CPUOK may be false until the second call.
func (m *Monitor) Sample() Stats {
	s := Stats{NumCPU: runtime.NumCPU()}
	m.sample(&s)
	return s
}

//go:build !linux && !darwin

package sysmon

// sample is a no-op on platforms without a dependency-free metrics source
// (e.g. Windows); every metric stays unavailable and the UI shows so.
func (m *Monitor) sample(s *Stats) {}

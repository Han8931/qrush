package sysmon

import "testing"

func TestClampPercent(t *testing.T) {
	cases := map[float64]float64{-5: 0, 0: 0, 50: 50, 100: 100, 130: 100}
	for in, want := range cases {
		if got := clampPercent(in); got != want {
			t.Errorf("clampPercent(%v) = %v, want %v", in, got, want)
		}
	}
}

// Sample always reports the core count regardless of platform support.
func TestSampleReportsNumCPU(t *testing.T) {
	if s := New().Sample(); s.NumCPU < 1 {
		t.Fatalf("NumCPU = %d, want >= 1", s.NumCPU)
	}
}

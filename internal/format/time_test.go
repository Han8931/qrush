package format

import "testing"

func TestDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{-1, ""},
		{0, "0s"},
		{500, "0s"},
		{1000, "1s"},
		{59999, "59s"},
		{60000, "1m0s"},
		{61000, "1m1s"},
		{3599000, "59m59s"},
		{3600000, "1h0m0s"},
		{3661000, "1h1m1s"},
		{86399000, "23h59m59s"},
		{86400000, "1d0h0m0s"},
		{90061000, "1d1h1m1s"},
	}

	for _, tt := range tests {
		got := Duration(tt.ms)
		if got != tt.want {
			t.Errorf("Duration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

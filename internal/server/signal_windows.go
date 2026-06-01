//go:build windows

package server

func IgnoreSIGPIPE() {
	// SIGPIPE does not exist on Windows
}

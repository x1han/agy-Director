//go:build !windows && !linux && !darwin

package procs

// isDarwin 在其它平台上恒为 false。
func isDarwin() bool { return false }

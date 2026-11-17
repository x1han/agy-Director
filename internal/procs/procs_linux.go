//go:build linux

package procs

// isDarwin 在 Linux 上恒为 false。
func isDarwin() bool { return false }

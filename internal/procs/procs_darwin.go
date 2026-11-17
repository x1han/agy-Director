//go:build darwin

package procs

// isDarwin 供 procs_unix.go 判断用 ps 的哪种参数形式。
func isDarwin() bool { return true }

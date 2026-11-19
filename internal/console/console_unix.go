//go:build !windows && !darwin

package console

// prepare 在 Linux 等平台上是空操作。
//
// Linux 下用户就是在一个已有的终端里跑 `agy-Director`，
// 不需要开新窗口、不需要改代码页。
//
// 真要重定向到文件（`agy-Director > out.txt`）时 bubbletea 会自己
// 检测到不是 tty 并退化成非交互模式，不会崩。
func prepare() (bool, func(), error) {
	return true, func() {}, nil
}

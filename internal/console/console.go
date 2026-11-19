// Package console 负责在 TUI 启动前把终端准备好。
//
// ── 为什么要单独一个包 ────────────────────────────────────────────────
//
// TUI 要在**终端里**渲染，所以必须先确认「当前进程有一个能用的终端」。
// 各平台的准备方式完全不同：
//
//   - Windows: 控制台代码页设成 UTF-8（不然中文是乱码）、设置窗口标题、
//     万一进程没有控制台就自己开一个。
//   - macOS:   从 .app bundle 双击启动时 stdout/stderr 接的是 /dev/null，
//     没有终端可渲染 —— 得先让 Terminal.app 开个窗口跑我们。
//   - Linux:   已经在终端里了，什么都不用做。
//
// 分平台拆文件是为了让每个平台的代码只在自己的 build tag 下编译。
package console

// Title 是终端窗口标题。
const Title = "agy-Director"

// Prepare 准备好终端环境。
//
// 返回 (ok, restore, err)：
//
//   - ok 为 false 表示**没能拿到可用的终端**（比如 Windows 上 AllocConsole
//     失败），调用方应当退化到纯文本输出，而不是直接崩掉。
//   - restore 是收尾函数，把进程改动过的终端设置还原
//     （Windows 上主要是控制台代码页 —— 那是控制台级别的设置，
//     不还原的话用户父 shell 之后的中文输出会变乱码）。
//     调用方**必须**在退出前调用它。
//   - err 只在"出问题了"时非 nil。
func Prepare() (bool, func(), error) {
	return prepare()
}

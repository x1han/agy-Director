// Package tui 实现 agy-Director 的终端图形界面。
//
// ── 界面要回答的三件事 ────────────────────────────────────────────────
//
//  1. 扫描出的 Antigravity 路径
//  2. 扫描出的可用通道
//  3. 缺 1 或 2 就明确说缺哪个、无法启动；
//     两样都有就问「是否启动：是（默认，回车直接启动）／否」
//
// ── 焦点模型 ──────────────────────────────────────────────────────────
//
// 光标初始停在最后一行「是否启动」，所以**直接回车就是启动**。
// 上下键可以往上走到目标行 / 通道行，在那两行用左右键切换候选项。
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/discovery"
)

// 选项类型。
type option int

const (
	// optNone 表示当前焦点不在任何可选项上。
	optNone option = iota
	// optTarget 表示焦点在 Antigravity 目标行。
	optTarget
	// optChannel 表示焦点在代理通道行。
	optChannel
	// optConfirm 表示焦点在「是否启动」行。
	optConfirm
)

// 颜色。
var (
	colAccent  = lipgloss.AdaptiveColor{Light: "#005F87", Dark: "#7DCFFF"}
	colOK      = lipgloss.AdaptiveColor{Light: "#00875F", Dark: "#04B575"}
	colErr     = lipgloss.AdaptiveColor{Light: "#AF0000", Dark: "#FF5F87"}
	colDim     = lipgloss.AdaptiveColor{Light: "#6C6C6C", Dark: "#9A9A9A"}
	colHeading = lipgloss.AdaptiveColor{Light: "#5F5F87", Dark: "#B0B0E0"}
	colPanel   = lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#3A3A3A"}
)

// 样式。
var (
	styTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styHeading  = lipgloss.NewStyle().Bold(true).Foreground(colHeading)
	styOK       = lipgloss.NewStyle().Foreground(colOK)
	styErr      = lipgloss.NewStyle().Bold(true).Foreground(colErr)
	styDim      = lipgloss.NewStyle().Foreground(colDim)
	styCursor   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stySelected = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styPanel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPanel).Padding(0, 1)
	styErrPanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colErr).Padding(0, 1)
)

// spinner 是扫描期间的动画帧。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ScanResult 是一次扫描的结果。
type ScanResult struct {
	// Targets 是找到的启动目标（桌面版 / CLI）。
	Targets []discovery.Target
	// Channels 是验证通过的通道。
	Channels []channel.Channel
	// ChannelResults 是全部通道的验证结果（含失败的），用于展示。
	ChannelResults []ChannelDetail
	// TargetErr 说明目标是怎么没的（没找到 / 出错）。
	TargetErr string
	// ChannelErr 说明通道是怎么没的。
	ChannelErr string
}

// ChannelDetail 是一个通道的详细信息。
type ChannelDetail struct {
	// Channel 是通道本身。
	Channel channel.Channel
	// OK 表示可用。
	OK bool
	// Detail 是验证结果描述。
	Detail string
}

// Ready 表示扫描结果是否足以启动。
func (s ScanResult) Ready() bool {
	return len(s.Targets) > 0 && len(s.Channels) > 0
}

// Model 是 bubbletea 的模型。
type Model struct {
	// result 是扫描结果。
	result ScanResult
	// scanning 为 true 时还在扫描。
	scanning bool
	// spin 是 spinner 的当前帧序号。
	spin int
	// width 是终端宽度。
	width int
	// focus 是当前焦点所在行。
	focus option
	// targetIdx 是选中的目标下标。
	targetIdx int
	// channelIdx 是选中的通道下标。
	channelIdx int
	// yes 是「是否启动」的当前值，默认 true。
	yes bool
	// quit 为 true 时退出程序。
	quit bool
	// accepted 为 true 表示用户确认了启动。
	accepted bool
	// err 是扫描过程中的错误。
	err error
}

// New 创建一个初始模型。
func New() Model {
	return Model{
		scanning: true,
		focus:    optConfirm,
		yes:      true,
	}
}

// Init 是 bubbletea 的初始命令。
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.scanCmd(), m.tickCmd())
}

// scanCmd 启动一次扫描。
func (m Model) scanCmd() tea.Cmd {
	return func() tea.Msg {
		res := doScan()
		return scanDoneMsg{result: res}
	}
}

// tickCmd 驱动 spinner 动画。
func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// scanDoneMsg 是扫描完成的消息。
type scanDoneMsg struct {
	result ScanResult
}

// tickMsg 是 spinner 的一帧。
type tickMsg time.Time

// Update 处理消息。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case scanDoneMsg:
		m.result = msg.result
		m.scanning = false
		// 扫描完了，光标停在「是否启动」上（默认是）。
		m.focus = optConfirm
		m.targetIdx = 0
		m.channelIdx = 0
		return m, nil

	case tickMsg:
		m.spin++
		if m.scanning {
			return m, m.tickCmd()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey 处理按键。
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 退出键任何时候都认 —— 扫描卡住时用户得有办法脱身。
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.quit = true
		m.accepted = false
		return m, tea.Quit
	}

	// 扫描中不接受其他交互：用户还不知道扫描结果，
	// 按 y 启动等于盲启动。
	if m.scanning {
		return m, nil
	}

	switch msg.String() {
	case "y", "Y":
		m.yes = true
		m.accepted = true
		m.quit = true
		return m, tea.Quit

	case "n", "N":
		m.yes = false
		m.accepted = false
		m.quit = true
		return m, tea.Quit

	case "up", "k":
		m.moveFocus(-1)
	case "down", "j":
		m.moveFocus(1)
	case "left", "h":
		m.cycle(-1)
	case "right", "l":
		m.cycle(1)
	case "enter":
		m.accepted = m.yes
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

// moveFocus 在「目标行 / 通道行 / 是否启动行」之间移动焦点。
//
// 只有实际存在的行才能被聚焦：没有找到目标就没有目标行，
// 没有可用通道就没有通道行。
func (m *Model) moveFocus(delta int) {
	rows := m.focusRows()
	if len(rows) == 0 {
		m.focus = optNone
		return
	}
	idx := 0
	for i, r := range rows {
		if r == m.focus {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	m.focus = rows[idx]
}

// focusRows 返回当前可以聚焦的行，从上到下。
func (m Model) focusRows() []option {
	var rows []option
	if len(m.result.Targets) > 0 {
		rows = append(rows, optTarget)
	}
	if len(m.result.Channels) > 0 {
		rows = append(rows, optChannel)
	}
	if m.result.Ready() {
		rows = append(rows, optConfirm)
	}
	return rows
}

// cycle 在当前焦点所在的那一行里循环切换候选项。
func (m *Model) cycle(delta int) {
	switch m.focus {
	case optTarget:
		if n := len(m.result.Targets); n > 1 {
			m.targetIdx = (m.targetIdx + delta + n) % n
		}
	case optChannel:
		if n := len(m.result.Channels); n > 1 {
			m.channelIdx = (m.channelIdx + delta + n) % n
		}
	case optConfirm:
		m.yes = !m.yes
	}
}

// Target 返回选中的启动目标。
func (m Model) Target() (discovery.Target, bool) {
	if m.targetIdx < 0 || m.targetIdx >= len(m.result.Targets) {
		return discovery.Target{}, false
	}
	return m.result.Targets[m.targetIdx], true
}

// Channel 返回选中的通道。
func (m Model) Channel() (channel.Channel, bool) {
	if m.channelIdx < 0 || m.channelIdx >= len(m.result.Channels) {
		return channel.Channel{}, false
	}
	return m.result.Channels[m.channelIdx], true
}

// View 渲染界面。
func (m Model) View() string {
	if m.scanning {
		return m.scanningView()
	}
	return m.resultView()
}

// scanningView 是扫描中的界面。
func (m Model) scanningView() string {
	frame := spinnerFrames[m.spin%len(spinnerFrames)]
	var b strings.Builder
	b.WriteString(styTitle.Render("agy-Director") + "  " + styDim.Render("正在扫描本机...") + "\n\n")
	b.WriteString(styCursor.Render(frame) + " 正在查找 Antigravity 与代理通道...\n")
	b.WriteString(styDim.Render("  （通道验证需要真实请求 Google 端点，约 1-10 秒）"))
	return b.String()
}

// resultView 是扫描完成后的界面。
func (m Model) resultView() string {
	var b strings.Builder

	// 标题栏
	head := styTitle.Render("agy-Director")
	if m.width > 0 {
		head += styDim.Render(strings.Repeat(" ", max(1, m.width-lipgloss.Width(head)-4)))
	}
	b.WriteString(styPanel.Render(head) + "\n\n")

	// Antigravity 目标
	b.WriteString(m.targetSection())
	b.WriteString("\n")

	// 代理通道
	b.WriteString(m.channelSection())
	b.WriteString("\n")

	// 提示 / 错误
	if !m.result.Ready() {
		b.WriteString(m.errorPanel())
		return b.String()
	}

	// 是否启动
	b.WriteString(m.confirmSection())
	b.WriteString("\n")
	b.WriteString(styDim.Render("  ↑↓ 切换行   ←→ 换选项   ⏎ 确认   y/n 直选   q 退出"))
	return b.String()
}

// targetSection 渲染 Antigravity 目标块。
func (m Model) targetSection() string {
	var b strings.Builder
	b.WriteString(styHeading.Render("Antigravity") + "\n")

	if len(m.result.Targets) == 0 {
		msg := m.result.TargetErr
		if msg == "" {
			msg = "未找到 Antigravity 桌面版，也未找到 agy CLI"
		}
		b.WriteString("  " + styErr.Render("✗ ") + styErr.Render(msg) + "\n")
		return b.String()
	}

	for i, t := range m.result.Targets {
		marker := "  "
		name := styDim.Render("  " + t.Kind.Label())
		if m.focus == optTarget && i == m.targetIdx {
			marker = styCursor.Render("▸ ")
			name = stySelected.Render("  " + t.Kind.Label())
		}
		// 被选中但焦点不在这一行时，用另一种标记区分。
		if i == m.targetIdx && m.focus != optTarget {
			marker = styDim.Render("▸ ")
		}

		b.WriteString(marker + name + "  " + t.Path + "\n")
		if t.Version != "" {
			b.WriteString(styDim.Render("       版本 "+t.Version) + "\n")
		}
	}
	return b.String()
}

// channelSection 渲染代理通道块。
func (m Model) channelSection() string {
	var b strings.Builder
	b.WriteString(styHeading.Render("代理通道") + "\n")

	if len(m.result.Channels) == 0 {
		msg := m.result.ChannelErr
		if msg == "" {
			msg = "未找到可用的代理通道"
		}
		b.WriteString("  " + styErr.Render("✗ ") + styErr.Render(msg) + "\n")
		return b.String()
	}

	for i, c := range m.result.Channels {
		marker := "  "
		name := styOK.Render("  " + c.Name)
		if m.focus == optChannel && i == m.channelIdx {
			marker = styCursor.Render("▸ ")
			name = stySelected.Render("  " + c.Name)
		}
		if i == m.channelIdx && m.focus != optChannel {
			marker = styDim.Render("▸ ")
		}

		b.WriteString(marker + name + "  " + c.URL() + "\n")
		if i == m.channelIdx {
			if detail := channelDetail(c); detail != "" {
				b.WriteString(styDim.Render("       "+detail) + "\n")
			}
		}
	}
	return b.String()
}

// channelDetail 返回通道的补充说明。
func channelDetail(c channel.Channel) string {
	var parts []string
	if c.Scheme != "" {
		parts = append(parts, string(c.Scheme))
	}
	if c.LAN {
		parts = append(parts, "局域网监听")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// errorPanel 渲染错误面板。
func (m Model) errorPanel() string {
	var b strings.Builder
	b.WriteString(styErrPanel.Render(styErr.Render("无法启动")) + "\n")
	for _, msg := range m.missingMessages() {
		b.WriteString("  " + styErr.Render("✗ "+msg) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(styDim.Render("  启动已中止。日志见 stderr（同时写入日志文件）。"))
	return b.String()
}

// missingMessages 列出缺了什么。
func (m Model) missingMessages() []string {
	var out []string
	if len(m.result.Targets) == 0 {
		msg := m.result.TargetErr
		if msg == "" {
			msg = "未找到 Antigravity 桌面版，也未找到 agy CLI"
		}
		out = append(out, msg)
	}
	if len(m.result.Channels) == 0 {
		msg := m.result.ChannelErr
		if msg == "" {
			msg = "未找到可用的代理通道"
		}
		out = append(out, msg)
	}
	return out
}

// confirmSection 渲染「是否启动」行。
func (m Model) confirmSection() string {
	yes, no := "是", "否"
	if m.yes {
		yes = stySelected.Render("▶ 是")
	} else {
		no = stySelected.Render("▶ 否")
	}
	return styHeading.Render("是否启动？") + "    " + yes + "   " + no
}

// max 返回两数较大者。
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

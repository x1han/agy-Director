package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/discovery"
)

// readyModel 造一个"扫描完成、结果齐全"的模型。
func readyModel() Model {
	m := New()
	m.scanning = false
	m.result = ScanResult{
		Targets: []discovery.Target{
			{Kind: discovery.TargetIDE, Path: "/opt/antigravity", Version: "2.33.0"},
			{Kind: discovery.TargetCLI, Path: "/usr/local/bin/agy"},
		},
		Channels: []channel.Channel{
			{Name: "v2rayN", Host: "127.0.0.1", Port: 10808, Scheme: channel.SchemeHTTP},
			{Name: "clash", Host: "127.0.0.1", Port: 7890, Scheme: channel.SchemeHTTP},
		},
	}
	m.focus = optConfirm
	return m
}

// missingModel 造一个"缺少目标"的模型。
func missingModel() Model {
	m := New()
	m.scanning = false
	m.result = ScanResult{
		TargetErr:  "未找到 Antigravity 桌面版",
		ChannelErr: "未找到可用的代理通道",
	}
	m.focus = optConfirm
	return m
}

// press 模拟按一个键。
func press(m Model, key string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

// pressSpecial 模拟按一个特殊键。
func pressSpecial(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestDefaultFocusIsConfirm(t *testing.T) {
	m := readyModel()
	if m.focus != optConfirm {
		t.Errorf("初始焦点应在「是否启动」行，实际是 %v", m.focus)
	}
	if !m.yes {
		t.Error("「是否启动」默认应为「是」")
	}
}

func TestEnterAcceptsByDefault(t *testing.T) {
	m := pressSpecial(readyModel(), "enter")
	if !m.accepted {
		t.Error("直接回车应当确认启动")
	}
	if !m.quit {
		t.Error("回车后应当退出 TUI")
	}
}

func TestYesNoKeys(t *testing.T) {
	m := press(readyModel(), "n")
	if m.accepted {
		t.Error("按 n 不应确认启动")
	}
	if !m.quit {
		t.Error("按 n 后应当退出 TUI")
	}

	m = press(readyModel(), "y")
	if !m.accepted {
		t.Error("按 y 应当确认启动")
	}
}

func TestQuitKeysReject(t *testing.T) {
	for _, key := range []string{"ctrl+c", "esc"} {
		m := pressSpecial(readyModel(), key)
		if m.accepted {
			t.Errorf("按 %s 不应确认启动", key)
		}
		if !m.quit {
			t.Errorf("按 %s 后应当退出 TUI", key)
		}
	}
}

func TestFocusMovement(t *testing.T) {
	m := readyModel()
	// 从「是否启动」往上 → 通道行
	m = pressSpecial(m, "up")
	if m.focus != optChannel {
		t.Errorf("上移一次应在通道行，实际是 %v", m.focus)
	}
	// 再上 → 目标行
	m = pressSpecial(m, "up")
	if m.focus != optTarget {
		t.Errorf("上移两次应在目标行，实际是 %v", m.focus)
	}
	// 已经在最上面，再上不动
	m = pressSpecial(m, "up")
	if m.focus != optTarget {
		t.Errorf("已在顶部时上移应不动，实际是 %v", m.focus)
	}
	// 一路往下回到「是否启动」
	m = pressSpecial(m, "down")
	m = pressSpecial(m, "down")
	if m.focus != optConfirm {
		t.Errorf("下移两次应回到「是否启动」行，实际是 %v", m.focus)
	}
	// 已经在最下面，再下不动
	m = pressSpecial(m, "down")
	if m.focus != optConfirm {
		t.Errorf("已在底部时下移应不动，实际是 %v", m.focus)
	}
}

func TestFocusSkipsMissingRows(t *testing.T) {
	// 只有通道、没有目标 → 可聚焦的行只有通道和「是否启动」
	m := New()
	m.scanning = false
	m.result = ScanResult{
		Channels: []channel.Channel{{Name: "v2rayN", Host: "127.0.0.1", Port: 10808}},
	}
	m.focus = optConfirm

	m = pressSpecial(m, "up")
	if m.focus != optChannel {
		t.Errorf("没有目标行时上移应落在通道行，实际是 %v", m.focus)
	}
	m = pressSpecial(m, "up")
	if m.focus != optChannel {
		t.Errorf("没有目标行时不应能聚焦到目标行，实际是 %v", m.focus)
	}
}

func TestCycleChannel(t *testing.T) {
	m := readyModel()
	m.focus = optChannel
	if m.channelIdx != 0 {
		t.Fatalf("通道下标初始应为 0，实际 %d", m.channelIdx)
	}
	m = pressSpecial(m, "right")
	if m.channelIdx != 1 {
		t.Errorf("右移一次通道下标应为 1，实际 %d", m.channelIdx)
	}
	// 循环回第一个
	m = pressSpecial(m, "right")
	if m.channelIdx != 0 {
		t.Errorf("循环右移后应回到 0，实际 %d", m.channelIdx)
	}
	// 反向
	m = pressSpecial(m, "left")
	if m.channelIdx != 1 {
		t.Errorf("左移一次通道下标应为 1，实际 %d", m.channelIdx)
	}
}

func TestCycleTarget(t *testing.T) {
	m := readyModel()
	m.focus = optTarget
	m = pressSpecial(m, "right")
	if m.targetIdx != 1 {
		t.Errorf("右移一次目标下标应为 1，实际 %d", m.targetIdx)
	}
}

func TestCycleDoesNothingWithSingleOption(t *testing.T) {
	m := New()
	m.scanning = false
	m.result = ScanResult{
		Targets:  []discovery.Target{{Kind: discovery.TargetIDE, Path: "/x"}},
		Channels: []channel.Channel{{Name: "only", Host: "127.0.0.1", Port: 1}},
	}
	m.focus = optChannel
	m = pressSpecial(m, "right")
	if m.channelIdx != 0 {
		t.Errorf("只有一个通道时不应切换，下标实际 %d", m.channelIdx)
	}
}

func TestConfirmRowTogglesYesNo(t *testing.T) {
	m := readyModel()
	m = pressSpecial(m, "left")
	if m.yes {
		t.Error("在「是否启动」行左移应切换为「否」")
	}
	m = pressSpecial(m, "right")
	if !m.yes {
		t.Error("再右移应切回「是」")
	}
}

func TestSelections(t *testing.T) {
	m := readyModel()
	tgt, ok := m.Target()
	if !ok {
		t.Fatal("应当能取到选中的目标")
	}
	if tgt.Path != "/opt/antigravity" {
		t.Errorf("选中的目标路径不对: %s", tgt.Path)
	}

	ch, ok := m.Channel()
	if !ok {
		t.Fatal("应当能取到选中的通道")
	}
	if ch.Name != "v2rayN" {
		t.Errorf("选中的通道名不对: %s", ch.Name)
	}
}

func TestViewShowsPathAndChannel(t *testing.T) {
	m := readyModel()
	view := m.View()
	for _, want := range []string{"/opt/antigravity", "v2rayN", "是否启动"} {
		if !strings.Contains(view, want) {
			t.Errorf("界面里应当出现 %q，实际输出:\n%s", want, view)
		}
	}
}

func TestViewShowsMissingMessages(t *testing.T) {
	m := missingModel()
	view := m.View()
	for _, want := range []string{"无法启动", "未找到 Antigravity 桌面版", "未找到可用的代理通道"} {
		if !strings.Contains(view, want) {
			t.Errorf("错误界面里应当出现 %q，实际输出:\n%s", want, view)
		}
	}
	// 缺东西时不该问「是否启动」
	if strings.Contains(view, "是否启动") {
		t.Errorf("缺东西时不该问「是否启动」，实际输出:\n%s", view)
	}
}

func TestReadyFlag(t *testing.T) {
	if !readyModel().result.Ready() {
		t.Error("结果齐全时 Ready 应为 true")
	}
	if missingModel().result.Ready() {
		t.Error("结果不全时 Ready 应为 false")
	}
}

func TestScanningViewShowsSpinner(t *testing.T) {
	m := New() // scanning = true
	view := m.View()
	if !strings.Contains(view, "扫描") {
		t.Errorf("扫描中应显示提示文字，实际输出:\n%s", view)
	}
}

func TestKeysIgnoredWhileScanning(t *testing.T) {
	m := New() // scanning = true
	m = press(m, "y")
	if m.accepted {
		t.Error("扫描中不应接受 y 键")
	}
}

func TestConfirmSectionShowsBothOptions(t *testing.T) {
	m := readyModel()
	view := m.confirmSection()
	// 选项部分必须正好是「是」和「否」两个。
	//
	// 注意不能直接数"是"字出现几次 —— 标题"是否启动？"里也有一个"是"。
	// 所以只取标题之后的部分来数。
	_, opts, found := strings.Cut(view, "？")
	if !found {
		t.Fatalf("「是否启动」行应当有标题，实际: %q", view)
	}
	if strings.Count(opts, "是") != 1 {
		t.Errorf("选项里「是」出现了 %d 次，实际: %q", strings.Count(opts, "是"), opts)
	}
	if strings.Count(opts, "否") != 1 {
		t.Errorf("选项里「否」出现了 %d 次，实际: %q", strings.Count(opts, "否"), opts)
	}
}

func TestConfirmSectionHighlightsYesByDefault(t *testing.T) {
	m := readyModel()
	view := m.confirmSection()
	// 高亮的是"是"（默认）。
	if !strings.Contains(view, "▶ 是") {
		t.Errorf("默认应高亮「是」，实际: %q", view)
	}
	m.yes = false
	view = m.confirmSection()
	if !strings.Contains(view, "▶ 否") {
		t.Errorf("选否时应高亮「否」，实际: %q", view)
	}
}

func TestChannelSelectionFollowsCursor(t *testing.T) {
	// 选中的通道必须跟着光标走 ——
	// 之前 Outcome 只带 Channel 列表，cmd 层永远取第一个，
	// 用户用 ←→ 换了通道却还是启动第一个。
	m := readyModel()
	m.focus = optChannel
	m = pressSpecial(m, "right")

	ch, ok := m.Channel()
	if !ok {
		t.Fatal("应当能取到选中的通道")
	}
	if ch.Name != "clash" {
		t.Errorf("右移后选中的通道应当是 clash，实际 %q", ch.Name)
	}
}

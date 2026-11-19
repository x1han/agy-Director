package tui

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/discovery"
)

// Outcome 是 TUI 跑完后的结果。
type Outcome struct {
	// Accepted 为 true 表示用户选了「是」。
	Accepted bool
	// Result 是扫描结果。
	Result ScanResult
	// Target 是选中的启动目标。Found 为 false 表示没有选中的。
	Target discovery.Target
	// Found 表示 Target 是否有效。
	Found bool
	// Channel 是选中的通道。
	Channel channel.Channel
	// ChannelFound 表示 Channel 是否有效。
	ChannelFound bool
}

// Run 跑一次 TUI，返回用户的选择。
//
// ⚠️ 调用方负责在跑之前静音 stderr 的日志（logging.MuteStderr），
// 跑完再恢复 —— 否则日志会和界面抢屏幕。
func Run() (Outcome, error) {
	p := tea.NewProgram(New(), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		switch {
		case errors.Is(err, tea.ErrInterrupted):
			// Ctrl+C 被 bubbletea 拦了，按"取消"处理。
			return Outcome{}, nil
		case errors.Is(err, tea.ErrProgramKilled):
			return Outcome{}, nil
		default:
			return Outcome{}, fmt.Errorf("TUI 运行失败: %w", err)
		}
	}

	m, ok := final.(Model)
	if !ok {
		return Outcome{}, errors.New("TUI 返回了非预期的模型类型")
	}

	res := Outcome{
		Accepted: m.accepted,
		Result:   m.result,
	}
	if t, ok := m.Target(); ok {
		res.Target = t
		res.Found = true
	}
	if c, ok := m.Channel(); ok {
		res.Channel = c
		res.ChannelFound = true
	}
	return res, nil
}

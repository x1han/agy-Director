package shellenv

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestReadFramedEnvTimeout_NeverCloses 验证 readFramedEnv 的超时的**硬上界**。
//
// 场景：用户的 rc 文件起了一个后台进程，它继承了 stdout 管道写端
// 并一直活着（典型的 ssh-agent / gpg-agent / 自定义守护）。
//
// 此时：
//   - 主 shell 已经 exit，管道写端却还开着 → 读端永远等不到 EOF；
//   - 读操作会**阻塞**在 Read 上；
//   - 早期版本把 deadline 检查放在循环顶部，卡在 Read 里时
//     那句检查根本走不到 → "超时"形同虚设，启动器被挂住。
//
// 现在 readFramedEnv 用定时器 + 打断来保证上界。
func TestReadFramedEnvTimeout_NeverCloses(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()

	// 只写起始分隔符，收尾分隔符永远不来；写端也永远不 Close。
	go func() {
		pw.Write([]byte(probeDelimiter + "\nHTTPS_PROXY=http://127.0.0.1:10808\n"))
	}()

	start := time.Now()
	_, err := readFramedEnv(pr, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("应该超时返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Errorf("错误信息应该说明是超时，实际: %v", err)
	}
	// 宽限是 readInterruptGrace(200ms)，留足余量。
	if max := 300*time.Millisecond + 2*time.Second; elapsed > max {
		t.Errorf("耗时 %v，超出上界 %v —— 阻塞读没被打断", elapsed, max)
	}
}

// TestReadFramedEnvTimeout_OSPipe 用**真的** os.Pipe 验证超时能打断
// 阻塞在 read(2) 里的读。
//
// 这是生产路径：cmd.StdoutPipe() 返回的就是 *os.File。
//
// ⚠️ 关键点：Unix 上 os.Pipe 的读端注册在 runtime poller 里，
// 所以 SetReadDeadline 能**唤醒**一个正阻塞的 read；
// 而单纯 close(fd) 是**唤醒不了**另一个线程里阻塞的 read 的
// （内核要等 read 返回才释放 fd）—— 这就是为什么
// interruptRead 优先用 deadline 而不是 close。
func TestReadFramedEnvTimeout_OSPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer w.Close()
	defer r.Close()

	// 写端保持打开（模拟后台任务持有写端）。
	if _, err := w.Write([]byte(probeDelimiter + "\nHTTPS_PROXY=x\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	start := time.Now()
	_, err = readFramedEnv(r, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("应该超时返回错误")
	}
	if max := 300*time.Millisecond + 2*time.Second; elapsed > max {
		t.Errorf("耗时 %v，超出上界 %v —— 阻塞在 read(2) 的读没被打破", elapsed, max)
	}
}

// TestReadFramedEnv_FastFrame 确认正常路径没有被超时机制拖慢：
// 帧一到就返回，不等 EOF。
func TestReadFramedEnv_FastFrame(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()

	go func() {
		pw.Write([]byte(probeDelimiter + "\nHTTPS_PROXY=http://127.0.0.1:10808\n" + probeDelimiter))
		// 故意不 Close —— 模拟持有 stdout 的后台任务。
	}()

	start := time.Now()
	got, err := readFramedEnv(pr, 5*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got, "HTTPS_PROXY=http://127.0.0.1:10808") {
		t.Errorf("帧内容不对: %q", got)
	}
	// 必须远早于 5s 就返回（不等 EOF）。
	if elapsed > 2*time.Second {
		t.Errorf("耗时 %v —— 不该等 EOF", elapsed)
	}
}

// TestReadFramedEnv_PartialThenFrame 确认分片到达也能正确取帧。
func TestReadFramedEnv_PartialThenFrame(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()

	go func() {
		pw.Write([]byte(probeDelimiter + "\nHTTPS_"))
		time.Sleep(50 * time.Millisecond)
		pw.Write([]byte("PROXY=http://127.0.0.1:10808\n"))
		time.Sleep(50 * time.Millisecond)
		pw.Write([]byte(probeDelimiter))
	}()

	got, err := readFramedEnv(pr, 5*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got, "HTTPS_PROXY=http://127.0.0.1:10808") {
		t.Errorf("帧内容不对: %q", got)
	}
}

// TestInterruptRead 验证打断机制。
//
// ⚠️ **平台差异（重要）**：
//
// Unix 上 os.Pipe 的读端注册在 runtime poller 里 → 支持
// SetReadDeadline → 能真正唤醒阻塞中的 read。
//
// Windows 上 os.Pipe 的句柄**没有**注册进 poller
// （os/file_windows.go 的 newFile 走 kind="pipe"，而
// poll.FD.Init 只对非阻塞句柄做 poller 关联，Windows 的
// syscall.Pipe 返回的句柄不满足）→ SetReadDeadline 返回
// ErrNoDeadline → 走 Close 兜底。
//
// 所以这个测试断言的是**"打断之后 readFramedEnv 必须返回"**，
// 而不是"底层 read 一定被唤醒"。后者在 Windows 上做不到，
// 但也不需要 —— readFramedEnv 的超时上界由**主流程的定时器**
// 保证，不依赖底层 read 醒来。
func TestInterruptRead(t *testing.T) {
	t.Run("os.Pipe 的阻塞读被打断后，上层必须按时返回", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		defer w.Close()
		defer r.Close()

		errCh := make(chan error, 1)
		go func() {
			buf := make([]byte, 16)
			_, err := r.Read(buf)
			errCh <- err
		}()

		// 给读一点时间进入阻塞态。
		time.Sleep(100 * time.Millisecond)
		interruptRead(r)

		// 底层读可能醒来，也可能不（Windows 上就醒不过来）。
		// 但**上层的 readFramedEnv 必须按时返回**，这才是关键。
		start := time.Now()
		_, err = readFramedEnv(r, 300*time.Millisecond)
		if err == nil {
			t.Fatal("应该超时返回错误")
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("耗时 %v，超出上界", elapsed)
		}
	})

	t.Run("不支持 deadline 的 reader → 退回 Close", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pw.Close()

		errCh := make(chan error, 1)
		go func() {
			buf := make([]byte, 16)
			_, err := pr.Read(buf)
			errCh <- err
		}()

		time.Sleep(100 * time.Millisecond)
		interruptRead(pr)

		select {
		case err := <-errCh:
			if err == nil {
				t.Error("被打断的读应该返回错误")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("打断后阻塞的读没有返回")
		}
	})
}

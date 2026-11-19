package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/x1han/agy-Director/internal/channel"
)

// useTempConfig 把配置根目录指向临时目录，避免碰到用户真实的配置。
//
// Windows 用 %APPDATA%，其余平台用 $XDG_CONFIG_HOME ——
// 两个都要设，免得测试跑到用户真实的配置上去。
func useTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	return dir
}

func TestLoadEmptyWhenNoFile(t *testing.T) {
	useTempConfig(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("文件不存在时不应报错: %v", err)
	}
	if len(cfg.Channels) != 0 {
		t.Errorf("文件不存在时应返回空配置，实际有 %d 个通道", len(cfg.Channels))
	}
}

func TestSaveAndLoad(t *testing.T) {
	useTempConfig(t)

	cfg := &Config{Preferred: "v2rayN"}
	if err := Save(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.Preferred != "v2rayN" {
		t.Errorf("首选通道不对: %q", got.Preferred)
	}
}

func TestLoadFallsBackToLegacyPath(t *testing.T) {
	dir := useTempConfig(t)

	// 造一个旧路径的配置文件。
	legacyDir := filepath.Join(dir, legacyAppName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyContent := `{"channels":[{"name":"old","host":"127.0.0.1","port":10808}],"preferred":"old"}`
	if err := os.WriteFile(filepath.Join(legacyDir, fileName), []byte(legacyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(cfg.Channels) != 1 {
		t.Fatalf("应当回退读到旧配置里的 1 个通道，实际 %d 个", len(cfg.Channels))
	}
	if cfg.Channels[0].Name != "old" {
		t.Errorf("读到的通道名不对: %q", cfg.Channels[0].Name)
	}
	if cfg.Preferred != "old" {
		t.Errorf("读到的首选通道不对: %q", cfg.Preferred)
	}
}

func TestNewPathWinsOverLegacy(t *testing.T) {
	dir := useTempConfig(t)

	// 旧路径
	legacyDir := filepath.Join(dir, legacyAppName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, fileName),
		[]byte(`{"channels":[{"name":"old","host":"127.0.0.1","port":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 新路径
	newDir := filepath.Join(dir, AppName)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, fileName),
		[]byte(`{"channels":[{"name":"new","host":"127.0.0.1","port":2}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].Name != "new" {
		t.Errorf("新路径应当优先，实际读到 %+v", cfg.Channels)
	}
}

func TestFind(t *testing.T) {
	useTempConfig(t)

	cfg := &Config{
		Channels:  []channel.Channel{{Name: "v2rayN", Host: "127.0.0.1", Port: 10808}},
		Preferred: "v2rayN",
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	if _, ok := Find("v2rayN"); !ok {
		t.Error("按名字应当找得到")
	}
	if _, ok := Find("127.0.0.1:10808"); !ok {
		t.Error("按地址应当找得到")
	}
	if _, ok := Find(""); !ok {
		t.Error("空名字应当落到首选通道")
	}
	if _, ok := Find("nope"); ok {
		t.Error("不存在的名字不应找得到")
	}
}

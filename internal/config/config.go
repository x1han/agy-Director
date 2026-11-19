// Package config 负责代理通道配置的读写。
//
// 配置位置（XDG 规范）：
//
//	Windows: %APPDATA%\agy-Director\channels.json
//	其余:    $XDG_CONFIG_HOME/agy-Director/channels.json
//	         （没设 XDG_CONFIG_HOME 就是 ~/.config/agy-Director/channels.json）
//
// ── 向后兼容 ──────────────────────────────────────────────────────────
//
// 项目原名 agy-untun，老版本把配置写在 %APPDATA%\agy-untun\channels.json。
// 改名后如果只读新路径，老用户的配置就"凭空消失"了。
// 所以 Load() 在新路径不存在时**回退去读旧路径**，
// 但 Save() 一律写新路径（下次保存就自动迁移过去了）。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/x1han/agy-Director/internal/channel"
)

const (
	// AppName 是配置目录名。
	AppName = "agy-Director"
	// legacyAppName 是改名前的目录名，用于读取旧配置。
	legacyAppName = "agy-untun"
	// fileName 是配置文件名。
	fileName = "channels.json"
)

// Config 是通道配置文件的结构。
type Config struct {
	// Channels 是已配置的通道列表。
	Channels []channel.Channel `json:"channels"`
	// Preferred 是默认通道名。
	Preferred string `json:"preferred,omitempty"`
}

// Dir 返回配置目录，并确保它存在。
func Dir() (string, error) {
	base, err := configBase()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, AppName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// File 返回配置文件的完整路径（不保证文件存在）。
func File() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// legacyFile 返回旧版本（agy-untun）的配置文件路径。
//
// 不创建目录 —— 只是找一下老文件还在不在。
func legacyFile() (string, error) {
	base, err := configBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, legacyAppName, fileName), nil
}

// configBase 返回配置根目录（不含应用名）。
func configBase() (string, error) {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return appdata, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return base, nil
}

// Load 读取通道配置。文件不存在时返回空配置，不算错误。
//
// 新路径不存在但旧路径存在时，读旧路径的内容。
func Load() (*Config, error) {
	path, err := File()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// 新路径没有 → 试试旧的。
		if legacy, lerr := legacyFile(); lerr == nil {
			lb, lerr := os.ReadFile(legacy)
			if lerr == nil {
				cfg := &Config{}
				if err := json.Unmarshal(lb, cfg); err != nil {
					return nil, fmt.Errorf("旧配置文件 %s 解析失败: %w", legacy, err)
				}
				return cfg, nil
			}
			if !os.IsNotExist(lerr) {
				return nil, lerr
			}
		}
		return &Config{}, nil
	}
	cfg := &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("配置文件 %s 解析失败: %w", path, err)
	}
	return cfg, nil
}

// Save 写回通道配置。一律写新路径。
func Save(cfg *Config) error {
	path, err := File()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Find 按名字或地址找配置里的通道。
//
// name 为空时用首选通道。
func Find(name string) (channel.Channel, bool) {
	cfg, err := Load()
	if err != nil {
		return channel.Channel{}, false
	}
	for _, c := range cfg.Channels {
		if c.Name == name || c.Addr() == name {
			return c, true
		}
	}
	if name == "" && cfg.Preferred != "" {
		for _, c := range cfg.Channels {
			if c.Name == cfg.Preferred {
				return c, true
			}
		}
	}
	return channel.Channel{}, false
}

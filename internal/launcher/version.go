package launcher

import (
	"encoding/json"
	"regexp"
)

// versionPattern 从 CDP 的 User-Agent 里提取 Antigravity 版本。
// 实测返回形如：
//   ... Antigravity/2.32.1 Chrome/152.0.7977.167 Electron/44.9.1 ...
var versionPattern = regexp.MustCompile(`Antigravity/([0-9]+\.[0-9]+\.[0-9]+)`)

// parseVersionFromUserAgent 从 /json/version 的 JSON 里取出版本。
func parseVersionFromUserAgent(body string) string {
	var payload struct {
		Browser   string `json:"Browser"`
		UserAgent string `json:"User-Agent"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	if m := versionPattern.FindStringSubmatch(payload.UserAgent); len(m) == 2 {
		return m[1]
	}
	// 退化：从 Browser 字段里找版本（"Chrome/152.0.7977.167"）。
	if m := regexp.MustCompile(`([0-9]+\.[0-9]+\.[0-9]+)`).FindStringSubmatch(payload.Browser); len(m) == 2 {
		return m[1]
	}
	return ""
}

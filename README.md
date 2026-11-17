# agy-untun

让 Google Antigravity（桌面版与 `agy` CLI）**不走 TUN**也能出网：启动前探测可用代理通道，注入进程级代理环境变量后再启动。

> 状态：架构设计阶段。详见 `docs/` 下的评审报告。

## 为什么需要它

Antigravity 的网络请求由 Go 后端 `language_server`（桌面版 `resources/bin/language_server.exe`，CLI 内嵌同源）发出。该后端**只从环境变量**解析代理，不读 Windows 注册表的系统代理，也不走 Electron 的 `--proxy-server`。因此不开 TUN 时，Go 后端会直连并被中断（`unexpected EOF`），而 UI 层看起来一切正常。

注入 `HTTP_PROXY` / `HTTPS_PROXY` 即可修复，不需要 TUN。

## 构建

```bash
go build -o agy-untun ./cmd/agy-untun
```

交叉编译：

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/agy-untun-linux-amd64   ./cmd/agy-untun
GOOS=linux   GOARCH=arm64 go build -o dist/agy-untun-linux-arm64   ./cmd/agy-untun
GOOS=darwin  GOARCH=amd64 go build -o dist/agy-untun-darwin-amd64  ./cmd/agy-untun
GOOS=darwin  GOARCH=arm64 go build -o dist/agy-untun-darwin-arm64  ./cmd/agy-untun
GOOS=windows GOARCH=amd64 go build -o dist/agy-untun.exe           ./cmd/agy-untun
GOOS=windows GOARCH=arm64 go build -o dist/agy-untun-arm64.exe     ./cmd/agy-untun
```

## 图标

图标资产统一放在 `assets/`：

| 文件 | 说明 |
|---|---|
| `assets/antigravity-one-color.svg` | Antigravity 单色标志（`#202124`），来源：Google 官方 |
| `assets/antigravity-tile.svg` | 上者 + 白底圆角矩形（启动器实际使用） |
| `assets/agy-old-man.svg` | 早期 Antigravity 品牌（机器人脸），备用 |
| `assets/agy-untun.ico` | 由 `antigravity-tile.svg` 生成的多尺寸 Windows ICO |

重新生成 ICO：

```bash
python tools/icon/build.py assets/antigravity-tile.svg assets/agy-untun.ico
```

构建脚本只用标准库 + 无头 Chrome，无需额外 Python 依赖。

## 许可

待定。

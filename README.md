# RTunnel — 基于 WebSocket 的可靠 TCP 隧道

## 简介
- RTunnel 通过 WebSocket 将 TCP 流量穿透到远端，实现客户端—服务端的端口转发与隧道代理。
- 支持 HTTP/HTTPS（WS/WSS），可选择在客户端验证服务器证书或跳过验证。
- 具有可靠传输与会话管理：数据分片、序列号、ACK、重传、发送窗口；基于 Ping/Pong 的 RTT/RTO 估计；心跳与自动重连。

## 适用场景
- 在仅支持 HTTP/HTTPS 的环境中，通过 WebSocket 隧道代理实现端口转发，可穿透防火墙、NAT 等网络限制。
- 在一些禁止SSH的算力平台容器中也能工作，即使其提供的WS连接不稳定。

## 特性
- 客户端/服务端双模式，自动识别输入为 URL 时进入客户端模式。
- TLS/WSS 支持：
  - 服务端可加载证书与私钥（`--cert`、`--key`）。
  - 客户端可启用证书校验（`--secure`/`-s`）。
- 可靠传输：应用层按序接收、乱序缓冲、ACK 确认、窗口控制、超时重传。
- 心跳与连接质量评估：Ping/Pong，RTT 与 RTO 动态计算。
- 会话管理：为每个本地 TCP 连接分配会话 ID，支持多连接并发。
- 调试日志控制：仅在 `--debug`/`-d` 开启时打印高频日志与错误；正常关闭（例如 `use of closed network connection`）在非调试模式下不提示。
- 选项可在参数任意位置使用：`-d`/`--debug` 与其他选项可出现在参数列表的开头、中间或结尾。

## 用法
- 服务端：
  - `rtunnel <target_addr> <listen_port> [options]`
  - 示例：
    - HTTP：`rtunnel localhost:22 8080`
    - HTTPS：`rtunnel localhost:22 8443 --cert server.crt --key server.key`
- 客户端：
  - `rtunnel <remote_url> <local_port> [options]`
  - 示例：
    - WS：`rtunnel ws://localhost:8080 2222`
    - WSS 验证证书：`rtunnel wss://localhost:8443 2222 --secure`

## 命令行选项
- `--secure`, `-s`：
  - 服务端：启用 TLS（需同时提供 `--cert` 与 `--key`）。
  - 客户端：启用证书验证（默认关闭，可使用 `--secure` 开启）。
- `--cert <file>`：服务端证书文件路径。
- `--key <file>`：服务端私钥文件路径。
- `--debug`, `-d`：开启调试日志，仅在该选项启用时打印高频日志与错误。
- `--help`：显示帮助信息。

## 地址格式
- 服务端目标地址：`<ip:port>` 或仅 `<port>`（默认 IP 为 `127.0.0.1`）。
- 服务端监听端口：`<port>`（绑定到 `0.0.0.0`）。
- 客户端本地绑定端口：`<port>`（绑定到 `127.0.0.1`）。

## 构建
- 依赖：Go 1.21+
- Windows 用户可直接运行 `build.bat`，生成以下目标：
  - Linux amd64：`build\rtunnel-linux`
  - Windows amd64：`build\rtunnel.exe`
  - macOS arm64（Apple Silicon，M1–M4）：`build\rtunnel-darwin-arm64`
- 手动构建示例：
  - Linux：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w"`
  - Windows：`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o rtunnel.exe`
  - macOS（Apple Silicon）：`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o rtunnel-darwin-arm64`

## 运行示例
- 服务端（HTTP）：`rtunnel 22 8080`
- 服务端（HTTPS）：`rtunnel 22 8443 --cert server.crt --key server.key`
- 客户端（WS）：`rtunnel ws://localhost:8080 2222`
- 客户端（WSS）：`rtunnel wss://localhost:8443 2222`

## Credit
https://github.com/zanjie1999/tcp-over-websocket 
- 本项目参考了该项目的实现。
- 该项目实现了ws的自动重连与简单重传，可保证多数情况下的可靠传输。
- 本项目实现了应用层的可靠传输协议，可保证100%情况下的可靠传输。
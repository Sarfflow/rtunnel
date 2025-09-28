@echo off
REM 设置编码
chcp 65001

REM 清理旧构建
del /q build\*

REM 创建构建目录
if not exist build mkdir build

REM 构建 Linux amd64
echo 正在构建 Linux amd64...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o build\rtunnel-linux

REM 构建 Windows amd64
echo 正在构建 Windows amd64...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o build\rtunnel.exe

REM 构建 macOS arm64 (Apple Silicon)
echo 正在构建 macOS arm64...
set CGO_ENABLED=0
set GOOS=darwin
set GOARCH=arm64
go build -ldflags="-s -w" -o build\rtunnel-darwin-arm64

echo 构建完成！

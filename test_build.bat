@echo off
cd /d D:\Project\Golang\src\rtunnel
echo Testing rtunnel build...
go mod tidy
go build -o rtunnel cmd/main.go
if %ERRORLEVEL% EQU 0 (
    echo Build successful!
    echo Run rtunnel with: rtunnel --help
) else (
    echo Build failed with error %ERRORLEVEL%
)
pause
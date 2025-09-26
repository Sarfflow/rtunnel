package tunnel

import (
    "fmt"
    "net"
    "os"
    "os/signal"
)

// Tunnel 接口，其发送和接收应被设计为可靠的
type Tunnel interface {
    Start() error
    Stop() error
    Send(message []byte) error
    Receive() ([]byte, error)
}

// 基于websocket的隧道结构体
type WSTunnel struct {
    ListenAddr string
    SigChan    chan os.Signal
}

func (t *WSTunnel) Stop() error {
    // 在这里添加优雅的关闭代码
    fmt.Println("Stopping tunnel...")
    return nil
}


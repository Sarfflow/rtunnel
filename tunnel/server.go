package tunnel

import (
	"fmt"
	"log"
	"net"
	"os"
)

// Server 结构体，包含基本的 WSTunnel 和具体的逻辑
type Server struct {
	WSTunnel
	TargetAddr string
}

// NewServer 创建一个新的服务器
func NewServer(target, listen string, sigChan chan os.Signal) *Server {
	return &Server{
		WSTunnel:   WSTunnel{ListenAddr: listen, SigChan: sigChan},
		TargetAddr: target,
	}
}

// Start 启动服务器监听并处理连接
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", ":"+s.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.ListenAddr, err)
	}
	defer listener.Close()

	log.Printf("Server listening on :%s, forwarding to %s", s.ListenAddr, s.TargetAddr)

	for {
		select {
		case <-s.SigChan:
			log.Println("Shutting down server...")
			return nil
		default:
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Error accepting connection: %v", err)
				continue
			}
			go s.handleConnection(conn)
		}
	}
}

// 处理连接并转发数据
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("New server connection from %s, forwarding to %s", conn.RemoteAddr(), s.TargetAddr)
	// 这里可以实现连接转发的逻辑
}

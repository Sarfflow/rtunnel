package tunnel

import (
	"fmt"
	"log"
	"net"
	"os"
)

// Client 结构体，包含 WSTunnel 和具体的客户端逻辑
type Client struct {
	WSTunnel
	URL string
}

// NewClient 创建一个新的客户端
func NewClient(url, localPort string, sigChan chan os.Signal) *Client {
	return &Client{
		WSTunnel: WSTunnel{ListenAddr: localPort, SigChan: sigChan},
		URL:      url,
	}
}

// Start 启动客户端逻辑，监听本地端口并处理连接
func (c *Client) Start() error {
	listener, err := net.Listen("tcp", ":"+c.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", c.ListenAddr, err)
	}
	defer listener.Close()

	log.Printf("Client listening on :%s, forwarding to %s", c.ListenAddr, c.URL)

	for {
		select {
		case <-c.SigChan:
			log.Println("Shutting down client...")
			return nil
		default:
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Error accepting connection: %v", err)
				continue
			}
			go c.toTunnel(conn)
			go c.fromTunnel(conn)
		}
	}
}

func (c *Client) toTunnel(conn net.Conn) {
	defer conn.Close()
	log.Printf("New client connection from %s, forwarding to %s", conn.RemoteAddr(), c.URL)
	
	for {
		// 这里可以实现从客户端读取数据并发送到 WebSocket 的逻辑
		buffer := make([]byte, 4096)
		n, err := conn.Read(buffer)
		if err != nil {
			log.Printf("Error reading from connection: %v", err)
			return
		}
		// 通过 WebSocket 发送数据到服务器
		log.Printf("Forwarding %d bytes to server %s", n, c.URL)
		c.WSTunnel.Send(buffer[:n])
	}
}

func (c *Client) fromTunnel(conn net.Conn) {
	defer conn.Close()
	for {
		// 这里可以实现从 WebSocket 读取数据并发送到客户端的逻辑
		message, err := c.WSTunnel.Receive()
		if err != nil {
			log.Printf("Error receiving from WebSocket: %v", err)
			return
		}
		// 发送消息到客户端
		_, err = conn.Write(message)
		if err != nil {
			log.Printf("Error sending to client: %v", err)
			return
		}
	}
}
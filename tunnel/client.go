package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"rtunnel/logging"
	"rtunnel/protocol"
)

type Client struct {
	ListenAddr string
	URL        string
	Insecure   bool // 是否跳过TLS证书验证

	// 会话管理
	sessions   map[string]*protocol.TCPConnSession
	sessionsMu sync.RWMutex

	// 退出控制
	listener net.Listener
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewClient 创建一个新的客户端
func NewClient(url, localPort string, insecure bool) *Client {
	return &Client{
		ListenAddr: localPort,
		URL:        url,
		Insecure:   insecure,
		sessions:   make(map[string]*protocol.TCPConnSession),
	}
}

// Start 启动客户端逻辑，监听本地端口并处理连接
func (c *Client) Start() error {
	listener, err := net.Listen("tcp", ":"+c.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", c.ListenAddr, err)
	}
	c.listener = listener
	c.stopCh = make(chan struct{})

	// 启动前测试 WS 连接，失败则直接返回
	log.Printf("Testing WebSocket connection to %s", c.URL)
	if err := c.testWSConnection(); err != nil {
		listener.Close()
		return fmt.Errorf("WebSocket connection test failed: %w", err)
	}

	log.Printf("Client listening on :%s, forwarding to %s", c.ListenAddr, c.URL)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if c.isStopped() {
				log.Printf("Client listener closed; shutting down")
				return nil
			}
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		sessionID := uuid.New().String()
		session := protocol.NewTCPConnSession(sessionID, nil, conn)

		log.Printf("[session %s] New connection from %s", session.ID, conn.RemoteAddr())

		// 记录会话
		c.sessionsMu.Lock()
		c.sessions[sessionID] = session
		c.sessionsMu.Unlock()

		// 会话结束后从 map 中移除
		go func(id string, s *protocol.TCPConnSession) {
			<-s.Closed
			c.sessionsMu.Lock()
			delete(c.sessions, id)
			c.sessionsMu.Unlock()
			log.Printf("[session %s] removed from client session map", id)
		}(sessionID, session)

		go c.wsManager(session)
	}
}

// goroutine为session提供可用的ws连接
func (c *Client) wsManager(session *protocol.TCPConnSession) {
	log.Printf("[session %s] wsManager started", session.ID)
	for {
		select {
		case <-session.Closed:
			log.Printf("[session %s] wsManager exiting: session closed", session.ID)
			return
		case <-session.RequireWS:

			for {
				log.Printf("[session %s] dialing new WS connection...", session.ID)
				wsConn, err := c.dialWS(session.ID)
				if err != nil {
					logging.Errorf("[session %s] failed to dial WS: %v", session.ID, err)
					time.Sleep(time.Second)
					continue
				}
				session.SetWS(wsConn)
				break
			}

			// 上次连接成功后等待1秒，避免瞬间建立多个连接
			time.Sleep(time.Second)
			select {
			case <-session.RequireWS:
			default:
			}
		}
	}
}

// dialWS 建立WS连接并在握手时传递UUID
func (c *Client) dialWS(sessionID string) (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer

	// 配置TLS选项
	if c.Insecure {
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	header := http.Header{}
	header.Set("X-Session-ID", sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(ctx, c.URL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", c.URL, err)
	}

	return conn, nil
}

func (c *Client) testWSConnection() error {
	// 建立 WebSocket 连接
	timeoutSig := time.After(5 * time.Second)
	closeSig := make(chan struct{}, 1)
	wsConn, err := c.dialWS("test-connection")
	if err != nil {
		return err
	}
	defer wsConn.Close()

	// 启动读取循环以触发 Pong 回调
	go func() {
		for {
			wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, _, err := wsConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// 使用 SetPongHandler 处理 Pong 消息并计算 RTT
	start := time.Now()
	var rtt time.Duration
	wsConn.SetPongHandler(func(appData string) error {
		// 计算 RTT
		rtt = time.Since(start)
		log.Printf("Successfully connected to remote server. RTT: %v", rtt)
		// 关闭连接
		wsConn.Close()
		select {
		case closeSig <- struct{}{}:
		default:
		}
		return nil
	})

	// 发送一个 Ping 消息
	if err := wsConn.WriteMessage(websocket.PingMessage, nil); err != nil {
		wsConn.Close()
		return fmt.Errorf("failed to send ping: %w", err)
	}

	// 等待 Pong 或 超时
	select {
	case <-closeSig:
		return nil
	case <-timeoutSig:
		wsConn.Close()
		return fmt.Errorf("ping timeout")
	}
}

// Stop 优雅停止客户端：关闭监听器并发送 FIN 关闭所有会话
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		// 关闭 listener，打断 Accept 阻塞
		if c.listener != nil {
			c.listener.Close()
		}
		// 关闭 stopCh
		close(c.stopCh)

		// 关闭所有会话，触发 FIN
		c.sessionsMu.RLock()
		for _, s := range c.sessions {
			s.Close()
		}
		c.sessionsMu.RUnlock()
	})
}

// isStopped 判断是否已触发停止
func (c *Client) isStopped() bool {
	select {
	case <-c.stopCh:
		return true
	default:
		return false
	}
}

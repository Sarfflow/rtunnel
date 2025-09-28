package tunnel

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"rtunnel/protocol"
    "rtunnel/logging"
)

// Server 结构体，包含基本的 WSTunnel 和具体的逻辑
type Server struct {
	ListenAddr string
	TargetAddr string
	CertFile   string
	KeyFile    string

	sessions   map[string]*protocol.TCPConnSession
	sessionsMu sync.RWMutex
	upgrader   websocket.Upgrader
}

// NewServer 创建一个新的服务器
func NewServer(target, listen string) *Server {
	return &Server{
		ListenAddr: listen,
		TargetAddr: target,
		sessions:   make(map[string]*protocol.TCPConnSession),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// Start 启动服务器监听并处理连接
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	server := &http.Server{
		Addr:    ":" + s.ListenAddr,
		Handler: mux,
	}

	log.Printf("Server listening on :%s, forwarding to %s", s.ListenAddr, s.TargetAddr)

	go s.cleanupInactiveSessions()

	if s.CertFile != "" && s.KeyFile != "" {
		log.Printf("Using TLS with cert: %s, key: %s", s.CertFile, s.KeyFile)
		return server.ListenAndServeTLS(s.CertFile, s.KeyFile)
	}

	return server.ListenAndServe()
}

// handleRequest 处理HTTP请求，区分静态页面和WebSocket
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		sessionID := r.Header.Get("X-Session-ID")
		switch sessionID {
		case "":
			log.Printf("No session ID in request headers")
			http.Error(w, "Session ID required", http.StatusBadRequest)
			return
        case "test-connection":
            // 处理测试连接请求：升级后启动读循环以便自动响应 Ping
            wsConn, err := s.upgrader.Upgrade(w, r, nil)
            if err != nil {
                log.Printf("Failed to upgrade to WebSocket: %v", err)
                return
            }
            logging.Debugf("WebSocket connection test succeeded")
            // 默认 PingHandler 会在读取时自动发送 Pong，这里保持一个简单的读循环
            go func() {
                defer wsConn.Close()
                for {
                    wsConn.SetReadDeadline(time.Now().Add(10 * time.Second))
                    if _, _, err := wsConn.ReadMessage(); err != nil {
                        return
                    }
                }
            }()
            return
        }

		wsConn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Failed to upgrade to WebSocket: %v", err)
			return
		}

		if err := s.handleSession(sessionID, wsConn); err != nil {
			log.Printf("[session %s] handle session error: %v", sessionID, err)
			wsConn.Close()
		}
		return
	}

	s.serveStaticPage(w, r)
}

// serveStaticPage 提供静态页面服务
func (s *Server) serveStaticPage(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(".", "index.html")

	http.ServeFile(w, r, indexPath)
}

// handleSession 处理session逻辑
func (s *Server) handleSession(sessionID string, wsConn *websocket.Conn) error {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	session, exists := s.sessions[sessionID]

	if !exists {
		tcpConn, err := net.Dial("tcp", s.TargetAddr)
		if err != nil {
			return fmt.Errorf("failed to connect to target %s: %w", s.TargetAddr, err)
		}

		session = protocol.NewTCPConnSession(sessionID, wsConn, tcpConn)
		s.sessions[sessionID] = session

		go func() {
			<-session.Closed
			s.removeSession(sessionID)
		}()

		log.Printf("[session %s] created new session", sessionID)
	} else {
		session.SetWS(wsConn)
		log.Printf("[session %s] updated WS connection", sessionID)
	}

	return nil
}

// removeSession 从map中移除session
func (s *Server) removeSession(sessionID string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if _, exists := s.sessions[sessionID]; exists {
		delete(s.sessions, sessionID)
		log.Printf("[session %s] removed from sessions map", sessionID)
	}
}

// cleanupInactiveSessions 定期清理不活跃的session
func (s *Server) cleanupInactiveSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupStaleSessions()
	}
}

// cleanupStaleSessions 清理超过5分钟不活跃的session
func (s *Server) cleanupStaleSessions() {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	timeout := 5 * time.Minute

	for sessionID, session := range s.sessions {
		select {
		case <-session.Closed:
			delete(s.sessions, sessionID)
			log.Printf("[session %s] removed closed session", sessionID)
		default:
			if time.Since(session.GetLastActive()) > timeout {
				log.Printf("[session %s] cleaning up inactive session", sessionID)
				session.Close()
				delete(s.sessions, sessionID)
			}
		}
	}
}

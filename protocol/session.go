package protocol

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type TCPConnSession struct {
	ID  string // UUID，唯一标识
	URL string // WebSocket 地址

	// --- WS 连接 ---
	connMu sync.RWMutex
	wsConn *websocket.Conn

	// --- 发送侧 ---
	sendQueue  chan []byte
	sendWindow map[uint64][]byte
	nextSeq    uint64
	ackedSeq   uint64

	// --- 接收侧 ---
	expectedSeq uint64
	recvBuffer  map[uint64][]byte
	recvQueue   chan []byte

	// --- 状态 ---
	closed     chan struct{}
	lastActive time.Time
	mu         sync.Mutex
}

// NewTCPConnSession 创建并启动一个 session
func NewTCPConnSession(id, url string) *TCPConnSession {
	s := &TCPConnSession{
		ID:         id,
		URL:        url,
		sendQueue:  make(chan []byte, 1024),
		sendWindow: make(map[uint64][]byte),
		recvBuffer: make(map[uint64][]byte),
		recvQueue:  make(chan []byte, 1024),
		closed:     make(chan struct{}),
		lastActive: time.Now(),
	}

	// 启动后台任务
	go s.maintainWS()
	return s
}

// --- 外部 API ---

func (s *TCPConnSession) Write(data []byte) error {
	select {
	case s.sendQueue <- data:
		return nil
	case <-s.closed:
		return errors.New("session closed")
	}
}

func (s *TCPConnSession) Read() ([]byte, error) {
	select {
	case data := <-s.recvQueue:
		return data, nil
	case <-s.closed:
		return nil, errors.New("session closed")
	}
}

func (s *TCPConnSession) Close() {
	select {
	case <-s.closed:
		// 已经关过了
	default:
		close(s.closed)
		s.connMu.Lock()
		if s.wsConn != nil {
			s.wsConn.Close()
		}
		s.connMu.Unlock()
	}
}

// --- 内部逻辑 ---

func (s *TCPConnSession) maintainWS() {
	for {
		select {
		case <-s.closed:
			return
		default:
		}

		// 尝试连接
		ws, _, err := websocket.DefaultDialer.Dial(s.URL, nil)
		if err != nil {
			log.Printf("[session %s] dial WS failed: %v", s.ID, err)
			time.Sleep(time.Second)
			continue
		}

		s.connMu.Lock()
		s.wsConn = ws
		s.connMu.Unlock()

		log.Printf("[session %s] WS connected", s.ID)

		// 启动读写 loop
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.readLoop(ws)
		}()
		go func() {
			defer wg.Done()
			s.writeLoop(ws)
		}()

		// 等到连接断开
		wg.Wait()

		s.connMu.Lock()
		if s.wsConn == ws {
			s.wsConn = nil
		}
		s.connMu.Unlock()

		log.Printf("[session %s] WS disconnected, retrying...", s.ID)
		time.Sleep(time.Second)
	}
}

func (s *TCPConnSession) readLoop(ws *websocket.Conn) {
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			log.Printf("[session %s] read err: %v", s.ID, err)
			return
		}
		pkt, err := Deserialize(msg)
		if err != nil {
			log.Printf("[session %s] bad packet: %v", s.ID, err)
			continue
		}
		s.handlePacket(ws, pkt)
	}
}

func (s *TCPConnSession) writeLoop(ws *websocket.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.closed:
			return
		case data := <-s.sendQueue:
			s.sendData(ws, data)
		case <-ticker.C:
			// 心跳
			ws.WriteMessage(websocket.PingMessage, []byte("ping"))
		}
	}
}

func (s *TCPConnSession) sendData(ws *websocket.Conn, data []byte) {
	s.mu.Lock()
	seq := s.nextSeq
	s.nextSeq++
	pkt := NewDataPacket(seq, data)
	s.sendWindow[seq] = pkt.Data
	s.mu.Unlock()

	raw := pkt.Serialize()

	s.connMu.RLock()
	defer s.connMu.RUnlock()
	if s.wsConn == ws {
		ws.WriteMessage(websocket.BinaryMessage, raw)
	}
}

func (s *TCPConnSession) handlePacket(ws *websocket.Conn, pkt *Packet) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastActive = time.Now()

	switch pkt.Flag {
	case PacketData:
		if pkt.Seq == s.expectedSeq {
			// 正好是期望的包，立即交付
			s.recvQueue <- pkt.Data
			s.expectedSeq++

			// 顺便把乱序缓存里的连续包交付
			for {
				data, ok := s.recvBuffer[s.expectedSeq]
				if !ok {
					break
				}
				delete(s.recvBuffer, s.expectedSeq)
				s.recvQueue <- data
				s.expectedSeq++
			}
		} else if pkt.Seq > s.expectedSeq {
			// 未来的包，缓存
			s.recvBuffer[pkt.Seq] = pkt.Data
		}
		// 始终回复 ack
		ack := NewAckPacket(s.expectedSeq - 1)
		s.sendPacket(ws, ack)

	case PacketAck:
		if pkt.Ack > s.ackedSeq {
			for seq := s.ackedSeq + 1; seq <= pkt.Ack; seq++ {
				delete(s.sendWindow, seq)
			}
			s.ackedSeq = pkt.Ack
		}

	case PacketFin:
		s.Close()
	}
}

func (s *TCPConnSession) sendPacket(ws *websocket.Conn, pkt *Packet) {
	raw := pkt.Serialize()
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	if s.wsConn == ws {
		ws.WriteMessage(websocket.BinaryMessage, raw)
	}
}

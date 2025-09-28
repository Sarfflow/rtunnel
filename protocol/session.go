package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"rtunnel/logging"
)

// TCPConnSession 表示 TCP <-> WS 会话
type TCPConnSession struct {
	ID        string
	Closed    chan struct{}
	RequireWS chan struct{} // 通知外部需要新的 WS 连接

	tcpConn      net.Conn
	tcpDataQueue chan *Packet

	// WS 管理
	wsConn *websocket.Conn
	wsCond *sync.Cond
	mu     sync.Mutex

	// 可靠传输
	windowQueue  chan struct{}
	sendWindow   map[uint64]*Packet
	sendAckQueue chan uint64
	nextSeq      uint64
	ackedSeq     uint64

	expectedSeq   uint64
	recvBuffer    map[uint64][]byte
	requireResend chan struct{}

	lastActive time.Time

	srtt     time.Duration
	rttvar   time.Duration
	rto      time.Duration
	rtoTimer *time.Timer
}

// NewTCPConnSession 初始化
func NewTCPConnSession(id string, ws *websocket.Conn, tcp net.Conn) *TCPConnSession {
	s := &TCPConnSession{
		ID:            id,
		RequireWS:     make(chan struct{}, 1),
		tcpConn:       tcp,
		wsConn:        ws,
		nextSeq:       1,
		ackedSeq:      0,
		expectedSeq:   1,
		tcpDataQueue:  make(chan *Packet, 128),  // 代发封包
		windowQueue:   make(chan struct{}, 128), // 窗口大小
		sendWindow:    make(map[uint64]*Packet),
		sendAckQueue:  make(chan uint64, 128),
		recvBuffer:    make(map[uint64][]byte),
		requireResend: make(chan struct{}, 1),
		Closed:        make(chan struct{}),
		lastActive:    time.Now(),
		srtt:          time.Millisecond * 100,
		rttvar:        time.Millisecond * 50,
		rto:           time.Millisecond * 1000,
	}
	s.wsCond = sync.NewCond(&s.mu)

	if ws != nil {
		ws.SetPongHandler(s.pongHandler)
	} else {
		select {
		case s.RequireWS <- struct{}{}:
			logging.Debugf("[session %s] initial RequireWS sent", s.ID)
		default:
		}
	}

	go s.readTCP()
	go s.readLoop()
	go s.writeLoop()
	return s
}

// SetWS 热替换 WS
func (s *TCPConnSession) SetWS(ws *websocket.Conn) {
	s.mu.Lock()
	if ws != nil {
		ws.SetPongHandler(s.pongHandler)
		s.wsConn = ws
		s.wsCond.Broadcast()
	} else {
		// 清空当前 WS 连接，确保 GetWS 会阻塞等待新连接
		s.wsConn = nil
		select {
		case s.RequireWS <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

// GetWS 阻塞直到 WS 可用。该函数不能在持有锁的情况下调用
func (s *TCPConnSession) GetWS() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.wsConn == nil {
		s.wsCond.Wait()
	}
	return s.wsConn
}

// Close 关闭会话
func (s *TCPConnSession) Close() {
	select {
	case <-s.Closed:
		return
	default:
		close(s.Closed)
		if s.tcpConn != nil {
			s.tcpConn.Close()
		}
		s.mu.Lock()
		if s.wsConn != nil {
			// 直接发FIN包通知远程关闭
			pkt := NewFinPacket()
			s.wsConn.WriteMessage(websocket.BinaryMessage, pkt.Serialize())
			s.wsConn.Close()
		}
		if s.rtoTimer != nil {
			s.rtoTimer.Stop()
		}
		s.mu.Unlock()
		s.SetWS(nil)
	}
}

// GetLastActive 获取最后活动时间
func (s *TCPConnSession) GetLastActive() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}

func (s *TCPConnSession) startRTO() {
	if s.rtoTimer != nil {
		s.rtoTimer.Stop()
	}
	s.rtoTimer = time.AfterFunc(s.rto, func() {
		// RTO 到期，触发重传
		select {
		case s.requireResend <- struct{}{}:
		default:
		}
	})
}

func (s *TCPConnSession) stopRTO() {
	if s.rtoTimer != nil {
		s.rtoTimer.Stop()
		s.rtoTimer = nil
		// 清除重传请求
		select {
		case <-s.requireResend:
		default:
		}
	}
}

func (s *TCPConnSession) sendPing(ws *websocket.Conn) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(time.Now().UnixNano()))
	return ws.WriteMessage(websocket.PingMessage, buf)
}

func (s *TCPConnSession) pongHandler(appData string) error {
	data := []byte(appData)
	if len(data) != 8 {
		return nil // 不是我们定义的格式，忽略
	}
	sentTime := int64(binary.BigEndian.Uint64(data))
	rtt := time.Since(time.Unix(0, sentTime))
	s.updateRTO(rtt)
	logging.Debugf("[session %s] RTT: %v", s.ID, rtt)
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
	return nil
}

// 只在收到 Pong 时调用，不用加锁
func (s *TCPConnSession) updateRTO(rtt time.Duration) {

	if s.srtt == 0 { // 第一次样本
		s.srtt = rtt
		s.rttvar = rtt / 2
		s.rto = s.srtt + 4*s.rttvar
		return
	}

	const (
		alpha = 0.125
		beta  = 0.25
	)

	s.rttvar = time.Duration((1-beta)*float64(s.rttvar) + beta*math.Abs(float64(s.srtt-rtt)))
	s.srtt = time.Duration((1-alpha)*float64(s.srtt) + alpha*float64(rtt))
	s.rto = s.srtt + 4*s.rttvar

	// 合理限制
	if s.rto < 200*time.Millisecond {
		s.rto = 200 * time.Millisecond
	}
	if s.rto > 60*time.Second {
		s.rto = 60 * time.Second
	}
}

// 专门用于从tcp读取数据并封包
func (s *TCPConnSession) readTCP() {
	buf := make([]byte, 16384)
	for {
		select {
		case <-s.Closed:
			return
		case s.windowQueue <- struct{}{}: // 发送队列有空间
			n, err := s.tcpConn.Read(buf)
			if err != nil {
				if isNormalClose(err) {
					logging.Debugf("[session %s] tcp read closed: %v", s.ID, err)
				} else {
					logging.Errorf("[session %s] tcp read error: %v", s.ID, err)
				}
				s.Close()
				return
			}
			pkt := NewDataPacket(s.nextSeq, buf[:n])
			s.tcpDataQueue <- pkt

			s.mu.Lock()
			s.sendWindow[s.nextSeq] = pkt
			s.nextSeq++
			s.lastActive = time.Now()
			s.mu.Unlock()
		}
	}
}

func (s *TCPConnSession) writeLoop() {
	heartbeatTicker := time.NewTicker(time.Minute)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-s.Closed:
			return
		case <-heartbeatTicker.C:
			ws := s.GetWS() // 阻塞直到 WS 可用
			logging.Debugf("[session %s] sending ping", s.ID)
			if err := s.sendPing(ws); err != nil {
				logging.Errorf("[session %s] ws ping error: %v", s.ID, err)
				ws.Close()
				s.SetWS(nil)
			}

		case <-s.requireResend:
			ws := s.GetWS()
			logging.Debugf("[session %s] resending unacked packets", s.ID)
			s.mu.Lock()
			for _, pkt := range s.sendWindow {
				if err := ws.WriteMessage(websocket.BinaryMessage, pkt.Serialize()); err != nil {
					logging.Errorf("[session %s] ws resend error: %v", s.ID, err)
					ws.Close()
					s.SetWS(nil)
					break
				}
			}
			s.mu.Unlock()

		case ack_id := <-s.sendAckQueue:
			ws := s.GetWS()
			logging.Debugf("[session %s] sending ACK for seq %d", s.ID, ack_id)
			ack := NewAckPacket(ack_id)
			if err := ws.WriteMessage(websocket.BinaryMessage, ack.Serialize()); err != nil {
				logging.Errorf("[session %s] ws ack write error: %v", s.ID, err)
				ws.Close()
				s.SetWS(nil)
			}

		case pkt := <-s.tcpDataQueue: // 数据
			ws := s.GetWS()
			if err := ws.WriteMessage(websocket.BinaryMessage, pkt.Serialize()); err != nil {
				logging.Errorf("[session %s] ws write error: %v", s.ID, err)
				ws.Close()
				s.SetWS(nil)
			}
		}
	}
}

func (s *TCPConnSession) readLoop() {
	for {
		select {
		case <-s.Closed:
			return
		default:
		}

		ws := s.GetWS() // 阻塞直到 WS 可用
		_, msg, err := ws.ReadMessage()
		if err != nil {
			if isNormalClose(err) {
				logging.Debugf("[session %s] ws read closed: %v", s.ID, err)
			} else {
				logging.Errorf("[session %s] ws read error: %v", s.ID, err)
			}
			ws.Close()
			s.SetWS(nil) // 触发阻塞等待重连
			continue
		}

		pkt, err := Deserialize(msg)
		if err != nil {
			logging.Errorf("[session %s] bad packet: %v", s.ID, err)
			continue
		}

		s.handlePacket(pkt)
	}
}

// --- 可靠传输逻辑 ---
func (s *TCPConnSession) handlePacket(pkt *Packet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActive = time.Now()

	switch pkt.Flag {
	case PacketData:
		if pkt.Seq == s.expectedSeq {
			if _, err := s.tcpConn.Write(pkt.Data); err != nil {
				if isNormalClose(err) {
					logging.Debugf("[session %s] tcp write closed: %v", s.ID, err)
				} else {
					logging.Errorf("[session %s] tcp write error: %v", s.ID, err)
				}
				s.Close()
				return
			}
			s.expectedSeq++

			// 写入缓冲区中已缓存的连续数据
			for {
				data, ok := s.recvBuffer[s.expectedSeq]
				if !ok {
					break
				}
				if _, err := s.tcpConn.Write(data); err != nil {
					if isNormalClose(err) {
						logging.Debugf("[session %s] tcp write closed: %v", s.ID, err)
					} else {
						logging.Errorf("[session %s] tcp write error: %v", s.ID, err)
					}
					s.Close()
					return
				}
				delete(s.recvBuffer, s.expectedSeq)
				s.expectedSeq++
			}
		} else if pkt.Seq > s.expectedSeq {
			// 顺序不对，缓存
			s.recvBuffer[pkt.Seq] = pkt.Data

			if len(s.recvBuffer) > 1024 {
				// 缓存过多，说明对方可能在乱发包，关闭连接
				logging.Errorf("[session %s] recvBuffer too large, closing session", s.ID)
				s.Close()
				return
			}
		}

		// 请求写Loop发送 ACK
		s.sendAckQueue <- s.expectedSeq - 1
		logging.Debugf("[session %s] received DATA seq %d, expectedSeq now %d", s.ID, pkt.Seq, s.expectedSeq)

	case PacketAck:
		if pkt.Ack > s.ackedSeq {
			for seq := s.ackedSeq + 1; seq <= pkt.Ack; seq++ {
				delete(s.sendWindow, seq)
				<-s.windowQueue // 释放窗口
			}
			s.ackedSeq = pkt.Ack
			if len(s.sendWindow) > 0 {
				// 还有未确认的包，重置定时器
				s.startRTO()
			} else {
				// 所有都确认了，停止定时器
				s.stopRTO()
			}
		} else {
			// 收到重复ACK立刻触发重传
			select {
			case s.requireResend <- struct{}{}:
			default:
			}
		}

	case PacketFin:
		s.Close()
	}
}

// isNormalClose 判断是否为正常连接关闭导致的错误
func isNormalClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	// WebSocket 的正常关闭码
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return true
	}
	// 常见的底层文案
	return strings.Contains(err.Error(), "use of closed network connection")
}

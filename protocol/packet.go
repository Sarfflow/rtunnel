package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	PacketData     byte = 1
	PacketAck      byte = 2
	PacketBindConn byte = 3
	PacketFin      byte = 4
)

const headerSize = 1 + 8 + 8 + 4 // flag + seq + ack + dataLen

type Packet struct {
	Flag byte
	Seq  uint64
	Ack  uint64
	Data []byte
}

// Serialize 零拷贝序列化（返回一个完整的 []byte）
func (p *Packet) Serialize() []byte {
	totalLen := headerSize + len(p.Data)
	buf := make([]byte, totalLen)

	// 写头
	buf[0] = p.Flag
	binary.BigEndian.PutUint64(buf[1:9], p.Seq)
	binary.BigEndian.PutUint64(buf[9:17], p.Ack)
	binary.BigEndian.PutUint32(buf[17:21], uint32(len(p.Data)))

	copy(buf[21:], p.Data)

	return buf
}

// Deserialize 零拷贝反序列化（Data 直接引用原始切片，无额外分配）
func Deserialize(raw []byte) (*Packet, error) {
	if len(raw) < headerSize {
		return nil, errors.New("packet too short")
	}

	p := &Packet{}
	p.Flag = raw[0]
	p.Seq = binary.BigEndian.Uint64(raw[1:9])
	p.Ack = binary.BigEndian.Uint64(raw[9:17])
	dataLen := binary.BigEndian.Uint32(raw[17:21])

	if len(raw) < headerSize+int(dataLen) {
		return nil, errors.New("invalid packet length")
	}

	// 零拷贝：Data 引用原始切片的一部分
	if dataLen > 0 {
		p.Data = raw[21 : 21+dataLen]
	} else {
		p.Data = nil
	}

	return p, nil
}

// 工厂方法
func NewDataPacket(seq uint64, data []byte) *Packet {
	return &Packet{Flag: PacketData, Seq: seq, Data: data}
}

func NewAckPacket(ack uint64) *Packet {
	return &Packet{Flag: PacketAck, Ack: ack}
}

func NewBindConnPacket(uuid []byte) *Packet {
	return &Packet{Flag: PacketBindConn, Data: uuid}
}

func NewFinPacket() *Packet {
	return &Packet{Flag: PacketFin}
}

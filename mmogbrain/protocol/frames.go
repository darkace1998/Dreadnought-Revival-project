package protocol

import (
	"bufio"
	"encoding/binary"
	"net"
)

type BufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func NewBufferedConn(conn net.Conn) *BufferedConn {
	return &BufferedConn{Conn: conn, reader: bufio.NewReader(conn)}
}

func (c *BufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *BufferedConn) Peek(n int) ([]byte, error) {
	return c.reader.Peek(n)
}

type AppFrame struct {
	MsgType   uint16
	RequestID [16]byte
	Payload   []byte
}

func ParseAppFrames(data []byte) ([]AppFrame, []byte) {
	var frames []AppFrame
	for {
		if len(data) < 22 {
			return frames, data
		}
		if data[0] != 0x67 || data[1] != 0x50 {
			next := bytesIndexMagic(data[1:])
			if next < 0 {
				return frames, nil
			}
			data = data[next+1:]
			continue
		}
		size := int(binary.LittleEndian.Uint16(data[2:4]))
		if size < 22 {
			data = data[2:]
			continue
		}
		if len(data) < size {
			return frames, data
		}
		var requestID [16]byte
		copy(requestID[:], data[6:22])
		payload := append([]byte(nil), data[22:size]...)
		frames = append(frames, AppFrame{
			MsgType:   binary.LittleEndian.Uint16(data[4:6]),
			RequestID: requestID,
			Payload:   payload,
		})
		data = data[size:]
	}
}

func bytesIndexMagic(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == 0x67 && data[i+1] == 0x50 {
			return i
		}
	}
	return -1
}

func IsHandshakePacket(data []byte) bool {
	if len(data) < 6 || data[0] != 0x67 || data[1] != 0x50 {
		return false
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	return msgType == 0x10
}

func IsDigestPacket(data []byte) bool {
	if len(data) < 6 || data[0] != 0x67 || data[1] != 0x50 {
		return false
	}
	msgType := binary.LittleEndian.Uint16(data[4:6])
	return msgType == 0x12
}

func SendSeedResponse(conn net.Conn, _ []byte) error {
	packet := make([]byte, 0, 38)
	packet = AppendHeader(packet, 0x26, 0x11)
	packet = append(packet, ServerSeed[:]...)
	packet = append(packet, ServerNonce[:]...)
	_, err := conn.Write(packet)
	return err
}

func SendConnectedPing(conn net.Conn, _ []byte) error {
	payload := []byte{
		0xa5, 0x5a, 0xa5, 0x5a, 0x3c, 0xc3, 0x3c, 0xc3,
		0x69, 0x96, 0x69, 0x96, 0x0f, 0xf0, 0x0f, 0xf0,
	}
	packet := make([]byte, 0, 22)
	packet = AppendHeader(packet, 0x16, 0x16)
	packet = append(packet, payload...)
	_, err := conn.Write(packet)
	return err
}

func AppendHeader(packet []byte, size uint16, msgType uint16) []byte {
	var header [6]byte
	binary.LittleEndian.PutUint16(header[0:2], 0x5067)
	binary.LittleEndian.PutUint16(header[2:4], size)
	binary.LittleEndian.PutUint16(header[4:6], msgType)
	return append(packet, header[:]...)
}

func BuildResponseFrame(requestID [16]byte, requestType uint16, payload []byte) []byte {
	payload = AppendRootEnd(payload)
	frameType := requestType&0x00ff | 0x0300
	frame := make([]byte, 0, 22+len(payload))
	frame = AppendHeader(frame, uint16(22+len(payload)), frameType)
	frame = append(frame, requestID[:]...)
	frame = append(frame, payload...)
	return frame
}

func IsPingFrame(frame AppFrame) bool {
	return frame.MsgType == 0x0300 && len(frame.Payload) == 1
}

func BuildPingResponseFrame(requestID [16]byte, pingPayload byte) []byte {
	frame := make([]byte, 0, 23)
	frame = AppendHeader(frame, 23, 0x0300)
	frame = append(frame, requestID[:]...)
	frame = append(frame, pingPayload)
	return frame
}

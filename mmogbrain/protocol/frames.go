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
				if data[len(data)-1] == 0x67 {
					return frames, data[len(data)-1:]
				}
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

// Frame flags, the high byte of a frame's type word. Read by the client's
// frame parser (0x142a64030):
//
//   - FrameFirst (0x100): the first frame of a response; the client frees
//     whatever it had accumulated under that request id (0x142a61110).
//   - FrameLast (0x200): the response is complete; only then is it handled.
//
// Frames in between carry neither, and the client APPENDS each frame's
// payload to the request's buffer (0x142a5a5c0, 0x7ff8-byte pages, up to 256
// of them). A single-frame response carries both -- which is all this server
// ever sent, so every response had to fit one frame, and one frame has to fit
// the client's 32 KB receive ring (allocated 0x8000 in 0x142a655a0; a frame
// is parsed only once it is complete in the ring, so a larger one never is).
const (
	FrameFirst uint16 = 0x0100
	FrameLast  uint16 = 0x0200
)

// MaxChunkFrame is the largest frame SplitResponseFrame emits: well inside
// the 32 KB ring, and below the ~26 KB single frames already proven live.
const MaxChunkFrame = 16 * 1024

// SplitResponseFrame splits a complete single-frame response (FrameFirst and
// FrameLast both set) whose size exceeds maxFrame into consecutive frames
// under the same request id: the first flagged FrameFirst, the last FrameLast,
// each at most maxFrame bytes. The client reassembles them into the original
// payload. Anything else -- a frame that fits, or one that is not a whole
// response -- is returned unchanged. The frame's own size field is ignored:
// it cannot represent a payload over 65513 bytes, and the payload is
// everything after the 22-byte header.
func SplitResponseFrame(frame []byte, maxFrame int) [][]byte {
	if len(frame) <= maxFrame || len(frame) < 22 || maxFrame <= 22 {
		return [][]byte{frame}
	}
	frameType := binary.LittleEndian.Uint16(frame[4:6])
	if frameType&(FrameFirst|FrameLast) != FrameFirst|FrameLast {
		return [][]byte{frame}
	}
	base := frameType &^ (FrameFirst | FrameLast)
	header := frame[:22]
	payload := frame[22:]
	chunk := maxFrame - 22
	var frames [][]byte
	for off := 0; off < len(payload); off += chunk {
		end := off + chunk
		if end > len(payload) {
			end = len(payload)
		}
		flags := base
		if off == 0 {
			flags |= FrameFirst
		}
		if end == len(payload) {
			flags |= FrameLast
		}
		out := make([]byte, 0, 22+end-off)
		out = append(out, header[0], header[1])
		out = binary.LittleEndian.AppendUint16(out, uint16(22+end-off))
		out = binary.LittleEndian.AppendUint16(out, flags)
		out = append(out, header[6:22]...)
		out = append(out, payload[off:end]...)
		frames = append(frames, out)
	}
	return frames
}

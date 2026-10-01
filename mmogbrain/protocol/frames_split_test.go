package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A response too large for the client's 32 KB receive ring goes out as
// several frames under one request id: first flagged 0x100, last 0x200, the
// payloads concatenating to the original -- the reassembly the client's frame
// parser (0x142a64030) performs.
func TestSplitResponseFrameReassembles(t *testing.T) {
	var id [16]byte
	copy(id[:], "0123456789abcdef")
	payload := make([]byte, 100000)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	frame := BuildResponseFrame(id, 0x0041, payload)
	whole := frame[22:] // includes the root end BuildResponseFrame appends

	parts := SplitResponseFrame(frame, MaxChunkFrame)
	if len(parts) < 2 {
		t.Fatalf("a %d-byte frame was not split", len(frame))
	}
	var got []byte
	for i, p := range parts {
		if len(p) > MaxChunkFrame {
			t.Errorf("part %d is %d bytes, over %d", i, len(p), MaxChunkFrame)
		}
		if p[0] != 0x67 || p[1] != 0x50 || int(binary.LittleEndian.Uint16(p[2:4])) != len(p) {
			t.Errorf("part %d header is wrong", i)
		}
		if !bytes.Equal(p[6:22], id[:]) {
			t.Errorf("part %d lost the request id", i)
		}
		flags := binary.LittleEndian.Uint16(p[4:6])
		if flags&0xff != 0x41 {
			t.Errorf("part %d lost the message type: %#x", i, flags)
		}
		wantFirst, wantLast := i == 0, i == len(parts)-1
		if (flags&FrameFirst != 0) != wantFirst || (flags&FrameLast != 0) != wantLast {
			t.Errorf("part %d of %d has flags %#x", i, len(parts), flags)
		}
		got = append(got, p[22:]...)
	}
	if !bytes.Equal(got, whole) {
		t.Fatal("the parts do not reassemble into the original payload")
	}
	// The parser reads them back as frames of one request.
	stream := bytes.Join(parts, nil)
	frames, rest := ParseAppFrames(stream)
	if len(frames) != len(parts) || len(rest) != 0 {
		t.Fatalf("parsed %d frames (%d bytes left), want %d", len(frames), len(rest), len(parts))
	}
}

func TestSplitResponseFrameLeavesSmallFramesAlone(t *testing.T) {
	var id [16]byte
	frame := BuildResponseFrame(id, 0x0041, make([]byte, 1000))
	if parts := SplitResponseFrame(frame, MaxChunkFrame); len(parts) != 1 || !bytes.Equal(parts[0], frame) {
		t.Fatal("a small frame was changed")
	}
}

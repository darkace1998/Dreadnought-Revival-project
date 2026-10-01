package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// A response too big for one frame leaves the server as several frames, each
// within the client's 32 KB receive ring, flagged first/last, that reassemble
// into the response -- what the client's frame parser (0x142a64030) expects.
func TestOversizedResponseGoesOutAsReassemblableFrames(t *testing.T) {
	var id [16]byte
	copy(id[:], "request-id-01234")
	payload := make([]byte, 90000)
	for i := range payload {
		payload[i] = byte(i)
	}
	frame := protocol.BuildResponseFrame(id, 0x41, payload)
	conn := &captureConn{}
	if err := writeMmogAppResponse(logrus.New(), conn, "test", id, "YA_PlayerGet", frame, nil, false, "w", "i"); err != nil {
		t.Fatal(err)
	}
	frames, rest := protocol.ParseAppFrames(conn.Bytes())
	if len(rest) != 0 || len(frames) < 2 {
		t.Fatalf("%d frames, %d bytes left over", len(frames), len(rest))
	}
	var got []byte
	for i, f := range frames {
		if f.RequestID != id {
			t.Errorf("frame %d lost the request id", i)
		}
		first, last := f.MsgType&protocol.FrameFirst != 0, f.MsgType&protocol.FrameLast != 0
		if first != (i == 0) || last != (i == len(frames)-1) {
			t.Errorf("frame %d of %d has flags %#x", i, len(frames), f.MsgType)
		}
		got = append(got, f.Payload...)
	}
	if !bytes.Equal(got, frame[22:]) {
		t.Fatal("the frames do not reassemble into the response")
	}
	// No frame on the wire may exceed the ring.
	wire := conn.Bytes()
	for off := 0; off < len(wire); {
		size := int(binary.LittleEndian.Uint16(wire[off+2 : off+4]))
		if size > protocol.MaxChunkFrame {
			t.Errorf("a %d-byte frame was sent", size)
		}
		off += size
	}

	// DN_FRAME_CHUNKING=0: one frame, as before.
	t.Setenv("DN_FRAME_CHUNKING", "0")
	conn = &captureConn{}
	if err := writeMmogAppResponse(logrus.New(), conn, "test", id, "YA_PlayerGet", protocol.BuildResponseFrame(id, 0x41, make([]byte, 20000)), nil, false, "w", "i"); err != nil {
		t.Fatal(err)
	}
	if frames, _ := protocol.ParseAppFrames(conn.Bytes()); len(frames) != 1 {
		t.Errorf("with chunking off, %d frames were sent", len(frames))
	}
}

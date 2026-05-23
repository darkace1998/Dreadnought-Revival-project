package main

import (
	"fmt"
	"testing"

	"github.com/dreadnought-ps/mmogbrain/protocol"
)

func TestQuickLoginSize(t *testing.T) {
	var reqID [16]byte
	payload := buildMmogLoginSuccessPayload()
	frame := protocol.BuildResponseFrame(reqID, 0x0320, payload)
	fmt.Printf("UserLogin payload size: %d, frame size: %d\n", len(payload), len(frame))

	// Also try 0x0320 vs different type
	frame2 := buildMmogLoginSuccessFrame(reqID, 0x0320)
	fmt.Printf("UserLogin frame2 size: %d\n", len(frame2))

	// Check rootEnd
	root := protocol.AppendRootEnd(nil)
	fmt.Printf("RootEnd: % x (%d bytes)\n", root, len(root))
}

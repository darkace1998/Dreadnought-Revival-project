package protocol

import (
	"encoding/binary"
)

func AppendObjectStart(b []byte, stack []int, name string) ([]byte, []int) {
	b = appendFieldNameAndType(b, name, 0x0c)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func AppendArrayStart(b []byte, stack []int, name string) ([]byte, []int) {
	b = appendFieldNameAndType(b, name, 0x0d)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func AppendUnnamedObjectStart(b []byte, stack []int) ([]byte, []int) {
	b = append(b, 0x00, 0x0c)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

// AppendUnnamedArrayStart writes an array that is itself an element of an
// enclosing array, i.e. one with no field name of its own.
//
// The tech tree document needs this: its root is an array whose elements are
// arrays (one per manufacturer), whose elements are the item objects.
func AppendUnnamedArrayStart(b []byte, stack []int) ([]byte, []int) {
	b = append(b, 0x00, 0x0d)
	stack = append(stack, len(b))
	b = append(b, 0, 0, 0, 0)
	return b, stack
}

func AppendObjectEnd(b []byte, stack []int) ([]byte, []int) {
	if len(stack) == 0 {
		return b, stack
	}
	start := stack[len(stack)-1]
	stack = stack[:len(stack)-1]
	b = append(b, 0x00, 0x0e)
	var offset [4]byte
	binary.LittleEndian.PutUint32(offset[:], uint32(start))
	b = append(b, offset[:]...)
	// A container's declared length covers its contents plus the 6-byte
	// terminator, measured from just AFTER the length field -- it does not
	// include the length field itself. The terminator separately carries the
	// absolute offset of the length field as a back-reference.
	//
	// Both halves are confirmed against frames the client itself sent us. In a
	// YA_AnalyticsEvent request the "payload" object's length field sits at
	// offset 113 and the terminator's back-reference is 0x71 = 113; the object
	// declares 600, and 113 + 4 + 600 + 6 = 723, exactly the frame's payload
	// size. Including the length field would give 719.
	//
	// Getting this wrong by +4 is not cosmetic: an over-long container swallows
	// the first bytes of whatever follows it, so only the LAST element of any
	// array parsed and every earlier sibling was silently lost.
	binary.LittleEndian.PutUint32(b[start:start+4], uint32(len(b)-start-4))
	return b, stack
}

func AppendRootEnd(b []byte) []byte {
	return append(b, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00)
}

func AppendStringField(b []byte, name string, value string) []byte {
	b = appendFieldNameAndType(b, name, 0x09)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	b = append(b, length[:]...)
	b = append(b, value...)
	return b
}

// AppendBytesField writes a raw byte-array field (tag 0x0a).
//
// The client reads these into a value node that keeps a pointer/length pair at
// +0x38/+0x40 rather than the string slot at +0x28/+0x30, so a byte array is a
// distinct wire type from a string and cannot be substituted with one. The
// layout is otherwise identical to a string field: <namelen><name>0x0a<u32
// length><bytes>.
//
// Confirmed from a captured YA_SaveCtAData request the client sent us:
//
//	04 "data" 0a 1d000000 15000000 789c ...
//
// i.e. field "data", tag 0x0a, 0x1d bytes, whose contents are a save blob
// (int32 uncompressed size followed by zlib data). This is the type the SGD
// and SCtA fields of YA_PlayerGet use.
func AppendBytesField(b []byte, name string, value []byte) []byte {
	b = appendFieldNameAndType(b, name, 0x0a)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	b = append(b, length[:]...)
	b = append(b, value...)
	return b
}

func AppendInt32Field(b []byte, name string, value int32) []byte {
	b = appendFieldNameAndType(b, name, 0x56)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(value))
	b = append(b, raw[:]...)
	return b
}

func AppendBoolField(b []byte, name string, value bool) []byte {
	b = appendFieldNameAndType(b, name, 0x05)
	if value {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

func AppendUnnamedStringField(b []byte, value string) []byte {
	b = append(b, 0x00, 0x09)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	b = append(b, length[:]...)
	b = append(b, value...)
	return b
}

func AppendUnnamedInt32Field(b []byte, value int32) []byte {
	b = append(b, 0x00, 0x56)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], uint32(value))
	b = append(b, raw[:]...)
	return b
}

func AppendUnnamedBoolField(b []byte, value bool) []byte {
	b = append(b, 0x00, 0x05)
	if value {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b
}

func AppendInt32ArrayField(b []byte, stack []int, name string, values []int32) ([]byte, []int) {
	b, stack = AppendArrayStart(b, stack, name)
	for _, value := range values {
		b = AppendUnnamedInt32Field(b, value)
	}
	b, stack = AppendObjectEnd(b, stack)
	return b, stack
}

func AppendBoolArrayField(b []byte, stack []int, name string, values []bool) ([]byte, []int) {
	b, stack = AppendArrayStart(b, stack, name)
	for _, value := range values {
		b = AppendUnnamedBoolField(b, value)
	}
	b, stack = AppendObjectEnd(b, stack)
	return b, stack
}

func AppendStringArrayField(b []byte, stack []int, name string, values []string) ([]byte, []int) {
	b, stack = AppendArrayStart(b, stack, name)
	for _, value := range values {
		b = AppendUnnamedStringField(b, value)
	}
	b, stack = AppendObjectEnd(b, stack)
	return b, stack
}

func appendFieldNameAndType(b []byte, name string, fieldType byte) []byte {
	if len(name) > 255 {
		panic("MMOG field name exceeds 255 bytes: " + name)
	}
	b = append(b, byte(len(name)))
	b = append(b, name...)
	b = append(b, fieldType)
	return b
}

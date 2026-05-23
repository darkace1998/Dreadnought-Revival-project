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

func AppendObjectEnd(b []byte, stack []int) ([]byte, []int) {
	if len(stack) == 0 {
		return b, stack
	}
	start := stack[len(stack)-1]
	stack = stack[:len(stack)-1]
	b = append(b, 0x00, 0x0e)
	binary.LittleEndian.PutUint32(b[start:start+4], uint32(len(b)-start))
	var offset [4]byte
	binary.LittleEndian.PutUint32(offset[:], uint32(start))
	b = append(b, offset[:]...)
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

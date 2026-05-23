package protocol

import (
	"encoding/binary"
	"strings"
)

func ExtractStringField(payload []byte, target string) string {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			return ""
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return ""
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return ""
			}
			value := string(payload[i : i+valueLen])
			i += valueLen
			if name == target {
				return value
			}
		case 0x05:
			if i >= len(payload) {
				return ""
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return ""
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return ""
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return ""
			}
			if name == target {
				return ""
			}
			nestedStart := i + 4
			nestedEnd := i + objectLen
			if value := ExtractStringField(payload[nestedStart:nestedEnd], target); value != "" {
				return value
			}
			i += objectLen
		default:
			return ""
		}
	}
	return ""
}

func ExtractInt32Field(payload []byte, target string) (int32, bool) {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			return 0, false
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return 0, false
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return 0, false
			}
			i += valueLen
		case 0x05:
			if i >= len(payload) {
				return 0, false
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return 0, false
			}
			value := int32(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if name == target {
				return value, true
			}
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return 0, false
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return 0, false
			}
			if name == target {
				return 0, false
			}
			nestedStart := i + 4
			nestedEnd := i + objectLen
			if value, ok := ExtractInt32Field(payload[nestedStart:nestedEnd], target); ok {
				return value, true
			}
			i += objectLen
		default:
			return 0, false
		}
	}
	return 0, false
}

func ExtractRequestName(payload []byte) string {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if nameLen == 0 {
			break
		}
		if i+nameLen+1 > len(payload) {
			break
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return ""
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return ""
			}
			value := string(payload[i : i+valueLen])
			i += valueLen
			if name == "RT" {
				return value
			}
		case 0x05:
			if i >= len(payload) {
				return ""
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return ""
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return ""
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return ""
			}
			i += objectLen
		default:
			return ExtractRequestNameFromText(payload)
		}
	}
	return ExtractRequestNameFromText(payload)
}

func ExtractRequestNameFromText(payload []byte) string {
	text := string(payload)
	idx := strings.Index(text, "YA_")
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(text) {
		c := text[end]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			end++
			continue
		}
		break
	}
	return text[idx:end]
}

func FirstStringField(payload []byte, names ...string) string {
	for _, name := range names {
		if value := ExtractStringField(payload, name); value != "" {
			return value
		}
	}
	return ""
}

func FirstInt32Field(payload []byte, fallback int32, names ...string) int32 {
	for _, name := range names {
		if value, ok := ExtractInt32Field(payload, name); ok {
			return value
		}
	}
	return fallback
}

func FirstNonEmptyString(payload []byte, names ...string) string {
	for _, name := range names {
		if value := ExtractStringField(payload, name); value != "" && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func FirstInt32(payload []byte, names ...string) int32 {
	for _, name := range names {
		if value, ok := ExtractInt32Field(payload, name); ok {
			return value
		}
	}
	return 0
}

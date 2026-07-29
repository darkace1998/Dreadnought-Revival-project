package protocol

import (
	"encoding/binary"
	"strings"
)

func ExtractStringField(payload []byte, target string) string {
	fields := ExtractStringFields(payload, target)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func ExtractStringFields(payload []byte, targets ...string) []string {
	if len(targets) == 0 {
		return nil
	}
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	var values []string
	extractStringFields(payload, targetSet, &values)
	return values
}

func extractStringFields(payload []byte, targets map[string]struct{}, values *[]string) bool {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if i+nameLen+1 > len(payload) {
			return false
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09:
			if i+4 > len(payload) {
				return false
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return false
			}
			value := string(payload[i : i+valueLen])
			i += valueLen
			if _, ok := targets[name]; ok && (name != "" || strings.TrimSpace(value) != "") {
				*values = append(*values, value)
			}
		case 0x0a:
			// Byte array: same length prefix as a string, but it is binary and
			// must never be surfaced as a string value. Skipping it correctly
			// matters because requests that carry one (YA_SaveGame,
			// YA_SaveCtAData) also carry fields we do read.
			if i+4 > len(payload) {
				return false
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return false
			}
			i += valueLen
		case 0x05:
			if i >= len(payload) {
				return false
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return false
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return false
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return false
			}
			nestedStart := i + 4
			nestedEnd := i + objectLen
			if !extractStringFields(payload[nestedStart:nestedEnd], targets, values) {
				return false
			}
			i += objectLen
		case 0x0e:
			if nameLen != 0 || i+4 > len(payload) {
				return false
			}
			start := binary.LittleEndian.Uint32(payload[i : i+4])
			i += 4
			if start == 0 {
				return true
			}
		default:
			return false
		}
	}
	return true
}

// ExtractBytesField returns the contents of a byte-array field (tag 0x0a).
// Used to pull the opaque save blobs out of YA_SaveGame / YA_SaveCtAData so
// they can be stored and handed back verbatim on the next login.
func ExtractBytesField(payload []byte, target string) ([]byte, bool) {
	for i := 0; i < len(payload); {
		nameLen := int(payload[i])
		i++
		if i+nameLen+1 > len(payload) {
			return nil, false
		}
		name := string(payload[i : i+nameLen])
		i += nameLen
		fieldType := payload[i]
		i++
		switch fieldType {
		case 0x09, 0x0a:
			if i+4 > len(payload) {
				return nil, false
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return nil, false
			}
			if fieldType == 0x0a && name == target {
				value := make([]byte, valueLen)
				copy(value, payload[i:i+valueLen])
				return value, true
			}
			i += valueLen
		case 0x05:
			if i >= len(payload) {
				return nil, false
			}
			i++
		case 0x56:
			if i+4 > len(payload) {
				return nil, false
			}
			i += 4
		case 0x0c, 0x0d:
			if i+4 > len(payload) {
				return nil, false
			}
			objectLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			if objectLen <= 0 || i+objectLen > len(payload) {
				return nil, false
			}
			if value, ok := ExtractBytesField(payload[i+4:i+objectLen], target); ok {
				return value, true
			}
			i += objectLen
		case 0x0e:
			if nameLen != 0 || i+4 > len(payload) {
				return nil, false
			}
			start := binary.LittleEndian.Uint32(payload[i : i+4])
			i += 4
			if start == 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return nil, false
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
		case 0x0a:
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
		case 0x0a:
			if i+4 > len(payload) {
				return ""
			}
			valueLen := int(binary.LittleEndian.Uint32(payload[i : i+4]))
			i += 4
			if valueLen < 0 || i+valueLen > len(payload) {
				return ""
			}
			i += valueLen
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

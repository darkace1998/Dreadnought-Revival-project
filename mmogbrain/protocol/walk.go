package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"math"
)

// Scalar is one scalar field of a document, in document order.
type Scalar struct {
	Name  string
	Tag   byte
	Num   int64 // integer, bool and floating values (floats truncated)
	Str   string
	IsStr bool
}

// Scalars flattens every scalar field of a document -- descending into
// objects and arrays -- in the order they appear, which is what lets a caller
// read the entries of an array ({ShipID, ShipXp}, {ShipID, ShipXp}, ...) that
// the single-field extractors cannot tell apart.
//
// Widths follow the client's tag-size function (0x142A62F00): 0x05 bool,
// 0x16/0x26 1 byte, 0x36/0x46 2, 0x56/0x66 4, 0x76/0x86 8, 0x57 float32,
// 0x77 float64, 0x02-0x04 16 bytes, 0x09 string and 0x0A bytes with a u32
// length, 0x0C/0x0D containers with a u32 length, ended by 00 0E + u32. An
// unknown tag stops the walk; what was read so far is returned.
func Scalars(payload []byte) []Scalar {
	var out []Scalar
	i := 0
	for i+1 < len(payload) {
		nameLen := int(payload[i])
		if nameLen == 0 && payload[i+1] == 0x0e { // a container's end marker
			i += 6
			continue
		}
		if i+1+nameLen >= len(payload) {
			break
		}
		name := string(payload[i+1 : i+1+nameLen])
		tag := payload[i+1+nameLen]
		i += 2 + nameLen
		fixed := func(n int) ([]byte, bool) {
			if i+n > len(payload) {
				return nil, false
			}
			v := payload[i : i+n]
			i += n
			return v, true
		}
		switch tag {
		case 0x0c, 0x0d:
			if _, ok := fixed(4); !ok { // descend: the contents follow inline
				return out
			}
		case 0x09, 0x0a:
			l, ok := fixed(4)
			if !ok {
				return out
			}
			v, ok := fixed(int(binary.LittleEndian.Uint32(l)))
			if !ok {
				return out
			}
			out = append(out, Scalar{Name: name, Tag: tag, Str: string(v), IsStr: true})
		case 0x01:
			out = append(out, Scalar{Name: name, Tag: tag})
		case 0x02, 0x03, 0x04:
			v, ok := fixed(16)
			if !ok {
				return out
			}
			// A GUID (0x02) reads as its 32 hex digits -- the form player ids
			// take everywhere else (YA_GetPlayerStatistics sends its pids so).
			if tag == 0x02 {
				out = append(out, Scalar{Name: name, Tag: tag, Str: hex.EncodeToString(v), IsStr: true})
			}
		case 0x05, 0x16, 0x26:
			v, ok := fixed(1)
			if !ok {
				return out
			}
			n := int64(v[0])
			if tag == 0x16 {
				n = int64(int8(v[0]))
			}
			out = append(out, Scalar{Name: name, Tag: tag, Num: n})
		case 0x36, 0x46:
			v, ok := fixed(2)
			if !ok {
				return out
			}
			n := int64(binary.LittleEndian.Uint16(v))
			if tag == 0x36 {
				n = int64(int16(binary.LittleEndian.Uint16(v)))
			}
			out = append(out, Scalar{Name: name, Tag: tag, Num: n})
		case 0x56, 0x66, 0x57:
			v, ok := fixed(4)
			if !ok {
				return out
			}
			u := binary.LittleEndian.Uint32(v)
			n := int64(int32(u))
			switch tag {
			case 0x66:
				n = int64(u)
			case 0x57:
				n = int64(math.Float32frombits(u))
			}
			out = append(out, Scalar{Name: name, Tag: tag, Num: n})
		case 0x76, 0x86, 0x77:
			v, ok := fixed(8)
			if !ok {
				return out
			}
			u := binary.LittleEndian.Uint64(v)
			n := int64(u)
			if tag == 0x77 {
				n = int64(math.Float64frombits(u))
			}
			out = append(out, Scalar{Name: name, Tag: tag, Num: n})
		default:
			return out
		}
	}
	return out
}

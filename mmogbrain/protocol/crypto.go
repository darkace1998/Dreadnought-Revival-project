package protocol

import (
	"crypto/md5"
	"encoding/hex"
)

var (
	ServerSeed  = [16]byte{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01}
	ServerNonce = [16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}
	SecretA     = MustDecode16("A32A61B2E749FCF9DAB79D9A4D1E0452")
	SecretB     = MustDecode16("F2AE945B4FE2A6840A81F891435796F3")
)

func DeriveSessionKey(clientNonce []byte) [16]byte {
	h := md5.New()
	h.Write(clientNonce)
	h.Write(ServerSeed[:])
	h.Write(SecretA[:])
	h.Write(SecretB[:])
	sum := h.Sum(nil)
	var key [16]byte
	copy(key[:], sum)
	return key
}

func MustDecode16(s string) [16]byte {
	decoded, err := hex.DecodeString(s)
	if err != nil || len(decoded) != 16 {
		panic("invalid MMOG secret")
	}
	var out [16]byte
	copy(out[:], decoded)
	return out
}

type StreamCipher struct {
	s        [256]byte
	i        byte
	j        byte
	feedback byte
}

func NewStreamCipher(key [16]byte, keyOffset int) *StreamCipher {
	c := &StreamCipher{}
	for i := 0; i < 256; i++ {
		c.s[i] = byte(i)
	}
	var j byte
	for i := 0; i < 256; i++ {
		j += c.s[i] + key[(i+keyOffset)&0x0f]
		c.s[i], c.s[j] = c.s[j], c.s[i]
	}
	return c
}

func (c *StreamCipher) Decrypt(data []byte) []byte {
	out := make([]byte, len(data))
	for idx, b := range data {
		keyByte := c.nextKeyByte()
		plain := keyByte ^ c.feedback ^ b
		c.feedback = plain
		out[idx] = plain
	}
	return out
}

func (c *StreamCipher) Encrypt(data []byte) []byte {
	out := make([]byte, len(data))
	for idx, b := range data {
		keyByte := c.nextKeyByte()
		out[idx] = keyByte ^ b ^ c.feedback
		c.feedback = b
	}
	return out
}

func (c *StreamCipher) nextKeyByte() byte {
	c.i++
	c.j += c.s[c.i]
	c.s[c.i], c.s[c.j] = c.s[c.j], c.s[c.i]
	return c.s[byte(c.s[c.i]+c.s[c.j])]
}

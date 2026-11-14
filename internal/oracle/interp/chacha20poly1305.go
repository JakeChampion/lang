package interp

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

func registerChaCha20Poly1305(i *Interp) {
	for _, seal := range []bool{true, false} {
		name := "__chacha20_poly1305_open_into"
		if seal {
			name = "__chacha20_poly1305_seal_into"
		}
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return chacha20Poly1305Into(name, args, seal) }}
	}
}

// chacha20Poly1305Tag is the tag of RFC 8439's AEAD over aad and ct under the
// one-time key otk.
func chacha20Poly1305Tag(otk, aad, ct []byte) []byte {
	st := make([]byte, 40)
	copy(st[24:], otk[:16])
	padded := func(b []byte) {
		full := len(b) - len(b)%16
		Poly1305Absorb(st, b[:full])
		if full != len(b) {
			var last [16]byte
			copy(last[:], b[full:])
			Poly1305Absorb(st, last[:])
		}
	}
	padded(aad)
	padded(ct)
	var lens [16]byte
	binary.LittleEndian.PutUint64(lens[:], uint64(len(aad)))
	binary.LittleEndian.PutUint64(lens[8:], uint64(len(ct)))
	Poly1305Absorb(st, lens[:])
	h0, s0 := binary.LittleEndian.Uint64(st), binary.LittleEndian.Uint64(otk[16:])
	t0 := h0 + s0
	carry := uint64(0)
	if t0 < h0 {
		carry = 1
	}
	t1 := binary.LittleEndian.Uint64(st[8:]) + binary.LittleEndian.Uint64(otk[24:]) + carry
	return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, t0), t1)
}

// chacha20Poly1305Into seals or opens buf[at:at+n] in place, as the compiled
// kernels do. The key stream of block ctr is the one-time key, and the text
// starts at block ctr + 1. buf[aad_at:aad_at+aad_n] is the additional data,
// and the tag is at buf[at+n:at+n+16]. An open leaves its verdict in
// buf[at+n]: 0 when the tag verifies and 1 when it does not.
func chacha20Poly1305Into(name string, args []Value, seal bool) (Value, error) {
	if len(args) != 7 {
		return nil, fmt.Errorf("%s: expected 7 args, got %d", name, len(args))
	}
	v, err := byteViews(name, args[0:3], 3)
	if err != nil {
		return nil, err
	}
	key, ctr, buf := v[0], v[1], v[2]
	var ints [4]int64
	for k, a := range args[3:] {
		n, ok := a.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: argument %d is not a number", name, k+3)
		}
		ints[k] = int64(n)
	}
	aadAt, aadN, at, n := ints[0], ints[1], ints[2], ints[3]
	size := int64(len(buf))
	if len(key) != 32 || len(ctr) != 16 || aadAt < 0 || aadN < 0 || at < 0 || n < 0 || at+n+16 > size || aadAt+aadN > size {
		return nil, fmt.Errorf("%s: range out of bounds", name)
	}
	otk := make([]byte, 32)
	chacha20XOR(key, ctr, otk)
	next := append([]byte(nil), ctr...)
	binary.LittleEndian.PutUint32(next, binary.LittleEndian.Uint32(ctr)+1)
	aad, text, tag := buf[aadAt:aadAt+aadN], buf[at:at+n], buf[at+n:at+n+16]
	if seal {
		chacha20XOR(key, next, text)
		copy(tag, chacha20Poly1305Tag(otk, aad, text))
		return byteArray(buf), nil
	}
	verdict := byte(0)
	if subtle.ConstantTimeCompare(chacha20Poly1305Tag(otk, aad, text), tag) != 1 {
		verdict = 1
	}
	chacha20XOR(key, next, text)
	buf[at+n] = verdict
	return byteArray(buf), nil
}

package interp

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

func registerChaCha20(i *Interp) {
	i.Builtins["__chacha20_xor_into"] = &Builtin{Fn: builtinChaCha20XorInto}
}

// chacha20Block writes the key stream block of RFC 8439 for the state words
// 4 to 15: the key, then the block counter and the nonce.
func chacha20Block(out *[64]byte, in *[16]uint32) {
	x := *in
	qr := func(a, b, c, d int) {
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 16)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 12)
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 8)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 7)
	}
	for r := 0; r < 10; r++ {
		qr(0, 4, 8, 12)
		qr(1, 5, 9, 13)
		qr(2, 6, 10, 14)
		qr(3, 7, 11, 15)
		qr(0, 5, 10, 15)
		qr(1, 6, 11, 12)
		qr(2, 7, 8, 13)
		qr(3, 4, 9, 14)
	}
	for k := range x {
		binary.LittleEndian.PutUint32(out[4*k:], x[k]+in[k])
	}
}

// builtinChaCha20XorInto XORs the ChaCha20 key stream over buf[at:at+n] and
// returns the buffer. The key is 32 bytes. The 16 bytes of ctr are the first
// block counter, little-endian, and then the nonce. The counter wraps at 2^32.
func builtinChaCha20XorInto(_ *Interp, args []Value) (Value, error) {
	const name = "__chacha20_xor_into"
	if len(args) != 5 {
		return nil, fmt.Errorf("%s: expected 5 args, got %d", name, len(args))
	}
	v, err := byteViews(name, args[0:3], 3)
	if err != nil {
		return nil, err
	}
	key, ctr, buf := v[0], v[1], v[2]
	at, ok1 := args[3].(Number)
	n, ok2 := args[4].(Number)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%s: the range is not two numbers", name)
	}
	if len(key) != 32 || len(ctr) != 16 || at < 0 || n < 0 || int64(at)+int64(n) > int64(len(buf)) {
		return nil, fmt.Errorf("%s: range out of bounds", name)
	}
	in := [16]uint32{0x61707865, 0x3320646e, 0x79622d32, 0x6b206574}
	for k := 0; k < 8; k++ {
		in[4+k] = binary.LittleEndian.Uint32(key[4*k:])
	}
	for k := 0; k < 4; k++ {
		in[12+k] = binary.LittleEndian.Uint32(ctr[4*k:])
	}
	data := buf[int(at) : int(at)+int(n)]
	var ks [64]byte
	for off := 0; off < len(data); off += 64 {
		chacha20Block(&ks, &in)
		in[12]++
		for k := 0; k < 64 && off+k < len(data); k++ {
			data[off+k] ^= ks[k]
		}
	}
	return byteArray(buf), nil
}

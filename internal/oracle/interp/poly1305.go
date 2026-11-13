package interp

import (
	"encoding/binary"
	"fmt"
	"math/big"
)

func registerPoly1305(i *Interp) {
	i.Builtins["__poly1305_blocks"] = &Builtin{Fn: builtinPoly1305Blocks}
}

// poly1305P is 2^130 - 5.
var poly1305P = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))

// Poly1305Absorb is the contract of __poly1305_blocks on its 40-byte state:
// h0, h1, h2, r0 and r1 as little-endian u64. Only the low 2 bits of h2 are
// read, and r is clamped as RFC 8439 clamps it. Each 16-byte block of msg is
// added with 2^128 and then multiplied by r, mod 2^130 - 5. A short last block
// gets a 1 byte after its end and no 2^128. h leaves fully reduced, and r is
// not written.
func Poly1305Absorb(st, msg []byte) {
	limb := func(at int) *big.Int { return new(big.Int).SetUint64(binary.LittleEndian.Uint64(st[at:])) }
	h := new(big.Int).Lsh(new(big.Int).SetUint64(binary.LittleEndian.Uint64(st[16:])&3), 128)
	h.Add(h, new(big.Int).Lsh(limb(8), 64))
	h.Add(h, limb(0))
	r0 := binary.LittleEndian.Uint64(st[24:]) & 0x0ffffffc0fffffff
	r1 := binary.LittleEndian.Uint64(st[32:]) & 0x0ffffffc0ffffffc
	r := new(big.Int).Lsh(new(big.Int).SetUint64(r1), 64)
	r.Add(r, new(big.Int).SetUint64(r0))
	for off := 0; off < len(msg); off += 16 {
		block := make([]byte, 17)
		k := copy(block[:16], msg[off:])
		block[k] = 1
		for i, j := 0, 16; i < j; i, j = i+1, j-1 {
			block[i], block[j] = block[j], block[i]
		}
		h.Add(h, new(big.Int).SetBytes(block))
		h.Mul(h, r)
		h.Mod(h, poly1305P)
	}
	h.Mod(h, poly1305P)
	var le [24]byte
	b := h.Bytes()
	for i := range b {
		le[i] = b[len(b)-1-i]
	}
	copy(st[0:24], le[:])
}

// builtinPoly1305Blocks absorbs msg[at:at+n] into the state st and returns it.
func builtinPoly1305Blocks(_ *Interp, args []Value) (Value, error) {
	const name = "__poly1305_blocks"
	if len(args) != 4 {
		return nil, fmt.Errorf("%s: expected 4 args, got %d", name, len(args))
	}
	v, err := byteViews(name, args[0:2], 2)
	if err != nil {
		return nil, err
	}
	st, msg := v[0], v[1]
	at, ok1 := args[2].(Number)
	n, ok2 := args[3].(Number)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%s: the range is not two numbers", name)
	}
	if len(st) != 40 || at < 0 || n < 0 || int64(at)+int64(n) > int64(len(msg)) {
		return nil, fmt.Errorf("%s: range out of bounds", name)
	}
	Poly1305Absorb(st, msg[int(at):int(at)+int(n)])
	return byteArray(st), nil
}

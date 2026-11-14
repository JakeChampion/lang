package interp

import (
	"fmt"
	"math/bits"
)

func registerScryptBlockMix(i *Interp) {
	i.Builtins["__scrypt_block_mix"] = &Builtin{Fn: builtinScryptBlockMix}
}

// ScryptOrder is the order of the 16 words in each 64-byte block that
// __scrypt_block_mix reads and writes: position k holds word ScryptOrder[k].
// In this order, the four rows of 4 positions are the diagonals of the
// Salsa20 matrix.
var ScryptOrder = [16]int{0, 5, 10, 15, 4, 9, 14, 3, 8, 13, 2, 7, 12, 1, 6, 11}

// Salsa208 is the Salsa20/8 core of RFC 7914: eight rounds, and then the
// input added to the output.
func Salsa208(in [16]uint32) [16]uint32 {
	x := in
	qr := func(a, b, c, d int) {
		x[b] ^= bits.RotateLeft32(x[a]+x[d], 7)
		x[c] ^= bits.RotateLeft32(x[b]+x[a], 9)
		x[d] ^= bits.RotateLeft32(x[c]+x[b], 13)
		x[a] ^= bits.RotateLeft32(x[d]+x[c], 18)
	}
	for range 4 {
		qr(0, 4, 8, 12)
		qr(5, 9, 13, 1)
		qr(10, 14, 2, 6)
		qr(15, 3, 7, 11)
		qr(0, 1, 2, 3)
		qr(5, 6, 7, 4)
		qr(10, 11, 8, 9)
		qr(15, 12, 13, 14)
	}
	for k := range x {
		x[k] += in[k]
	}
	return x
}

// scryptWords reads the block of 8 slots at b[at:] into words in RFC order.
// Slot k holds position 2k in its low half and position 2k+1 in its high half.
func scryptWords(b []uint64, at int) [16]uint32 {
	var w [16]uint32
	for k := range 8 {
		w[ScryptOrder[2*k]] = uint32(b[at+k])
		w[ScryptOrder[2*k+1]] = uint32(b[at+k] >> 32)
	}
	return w
}

func scryptPut(b []uint64, at int, w [16]uint32) {
	for k := range 8 {
		b[at+k] = uint64(w[ScryptOrder[2*k]]) | uint64(w[ScryptOrder[2*k+1]])<<32
	}
}

// ScryptBlockMix is the contract of __scrypt_block_mix: the BlockMix of RFC
// 7914 of b[from:from+16r] ^ b[mix:mix+16r], written to b[to:to+16r]. The
// even outputs go to the first half of the output and the odd outputs to the
// second half.
func ScryptBlockMix(b []uint64, from, mix, to, r int) {
	x := scryptWords(b, from+16*r-8)
	m := scryptWords(b, mix+16*r-8)
	for k := range x {
		x[k] ^= m[k]
	}
	for i := range 2 * r {
		in, m := scryptWords(b, from+8*i), scryptWords(b, mix+8*i)
		for k := range x {
			x[k] ^= in[k] ^ m[k]
		}
		x = Salsa208(x)
		scryptPut(b, to+8*(i/2+(i%2)*r), x)
	}
}

// ScryptBlockMixFits reports whether the three blocks of 16r slots lie in an
// array of n slots, and whether the output block is apart from both inputs.
func ScryptBlockMixFits(n, from, mix, to, r int64) bool {
	if r < 1 || from < 0 || mix < 0 || to < 0 {
		return false
	}
	h := 16 * r
	if from+h > n || mix+h > n || to+h > n {
		return false
	}
	apart := func(a, b int64) bool { return a-b >= h || b-a >= h }
	return apart(to, from) && apart(to, mix)
}

func builtinScryptBlockMix(_ *Interp, args []Value) (Value, error) {
	const name = "__scrypt_block_mix"
	if len(args) != 5 {
		return nil, fmt.Errorf("%s: expected b, from, mix, to and r, got %d args", name, len(args))
	}
	b, err := limbView(name, args[0])
	if err != nil {
		return nil, err
	}
	var at [4]int64
	for k, a := range args[1:] {
		n, ok := a.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: argument %d is not an i32", name, k+2)
		}
		at[k] = int64(n)
	}
	if !ScryptBlockMixFits(int64(len(b)), at[0], at[1], at[2], at[3]) {
		return nil, fmt.Errorf("%s: a block is outside the array or the output overlaps an input", name)
	}
	ScryptBlockMix(b, int(at[0]), int(at[1]), int(at[2]), int(at[3]))
	out := newArray(len(b))
	for k, w := range b {
		out.Elems()[k] = Number(int64(w))
	}
	return out, nil
}

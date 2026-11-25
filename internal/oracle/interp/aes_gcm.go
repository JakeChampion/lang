package interp

import (
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"sync"
)

func registerAESGCM(i *Interp) {
	i.Builtins["__aes_expand_key"] = &Builtin{Fn: builtinAESExpandKey}
	i.Builtins["__aes_ctr32"] = &Builtin{Fn: builtinAESCtr32}
	i.Builtins["__aes_dec_blocks"] = &Builtin{Fn: builtinAESDecBlocks}
	i.Builtins["__ghash"] = &Builtin{Fn: builtinGHASH}
	i.Builtins["__aes_gcm_seal_into"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return aesGCMInto("__aes_gcm_seal_into", args, true) }}
	i.Builtins["__aes_gcm_open_into"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return aesGCMInto("__aes_gcm_open_into", args, false) }}
	for _, name := range []string{"__sha256_hw", "__sha1_hw", "__sha1x8_hw", "__sha256x8_hw"} {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Bool(false), nil }}
	}
	i.Builtins["__sha256_hw_digest_block"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if _, err := byteViews("__sha256_hw_digest_block", args, 2); err != nil {
			return nil, err
		}
		return args[0], nil
	}}
	for _, name := range []string{"__sha256_hw_blocks", "__sha1_hw_blocks", "__sha1x8_hw_blocks", "__sha256x8_hw_blocks"} {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if _, err := byteViews(name, args, 2); err != nil {
				return nil, err
			}
			return newArray(0), nil
		}}
	}
}

func byteView(name string, v Value) ([]byte, error) {
	switch x := v.(type) {
	case String:
		return []byte(string(x)), nil
	case Array:
		if b, ok := x.Bytes(); ok {
			return append([]byte(nil), b...), nil
		}
		out := make([]byte, len(x.Elems()))
		for at, e := range x.Elems() {
			b, ok := e.(Number)
			if !ok || b < 0 || b > 255 {
				return nil, fmt.Errorf("%s: element %d is not u8", name, at)
			}
			out[at] = byte(b)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: expected a [u8], got %T", name, v)
}

func byteArray(b []byte) Array {
	return bytesBox(append(make([]byte, 0, len(b)), b...))
}

func byteViews(name string, args []Value, n int) ([][]byte, error) {
	if len(args) != n {
		return nil, fmt.Errorf("%s: expected %d args, got %d", name, n, len(args))
	}
	out := make([][]byte, n)
	for at, a := range args {
		b, err := byteView(name, a)
		if err != nil {
			return nil, err
		}
		out[at] = b
	}
	return out, nil
}

func gfMul(a, b byte) byte {
	var p byte
	for b != 0 {
		if b&1 != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1b
		}
		b >>= 1
	}
	return p
}

func sbox(x byte) byte {
	inv := byte(0)
	if x != 0 {
		inv = 1
		for k := 0; k < 254; k++ {
			inv = gfMul(inv, x)
		}
	}
	s := inv
	for k := 1; k <= 4; k++ {
		s ^= inv<<k | inv>>(8-k)
	}
	return s ^ 0x63
}

func aesExpandKey(key []byte) []byte {
	nk := len(key) / 4
	nr := nk + 6
	w := make([]byte, 16*(nr+1))
	copy(w, key)
	rcon := byte(1)
	for i := nk; i < 4*(nr+1); i++ {
		t := [4]byte{w[4*i-4], w[4*i-3], w[4*i-2], w[4*i-1]}
		if i%nk == 0 {
			t = [4]byte{sbox(t[1]) ^ rcon, sbox(t[2]), sbox(t[3]), sbox(t[0])}
			rcon = gfMul(rcon, 2)
		} else if nk > 6 && i%nk == 4 {
			t = [4]byte{sbox(t[0]), sbox(t[1]), sbox(t[2]), sbox(t[3])}
		}
		for k := 0; k < 4; k++ {
			w[4*i+k] = w[4*(i-nk)+k] ^ t[k]
		}
	}
	return w
}

func builtinAESExpandKey(_ *Interp, args []Value) (Value, error) {
	v, err := byteViews("__aes_expand_key", args, 1)
	if err != nil {
		return nil, err
	}
	if len(v[0]) != 16 && len(v[0]) != 24 && len(v[0]) != 32 {
		return nil, fmt.Errorf("__aes_expand_key: a key of %d bytes", len(v[0]))
	}
	return byteArray(aesExpandKey(v[0])), nil
}

var aesSbox = sync.OnceValue(func() [256]byte {
	var t [256]byte
	for x := range t {
		t[x] = sbox(byte(x))
	}
	return t
})

// roundKeyBlock is AES over the round keys of the caller, as the compiled
// kernels use them. It does not make the schedule again from the first bytes.
type roundKeyBlock struct {
	rk     []byte
	rounds int
}

func newRoundKeyBlock(rk []byte) (roundKeyBlock, bool) {
	switch {
	case len(rk) >= 240:
		return roundKeyBlock{rk, 14}, true
	case len(rk) >= 208:
		return roundKeyBlock{rk, 12}, true
	case len(rk) >= 176:
		return roundKeyBlock{rk, 10}, true
	}
	return roundKeyBlock{}, false
}

func (b roundKeyBlock) BlockSize() int { return 16 }

func (b roundKeyBlock) Decrypt(dst, src []byte) {
	sb := aesSbox()
	var inv [256]byte
	for x, y := range sb {
		inv[y] = byte(x)
	}
	var s [16]byte
	for i := range s {
		s[i] = src[i] ^ b.rk[16*b.rounds+i]
	}
	for r := b.rounds - 1; r >= 0; r-- {
		var t [16]byte
		for c := 0; c < 4; c++ {
			for row := 0; row < 4; row++ {
				t[4*((c+row)%4)+row] = inv[s[4*c+row]] ^ b.rk[16*r+4*((c+row)%4)+row]
			}
		}
		if r > 0 {
			for c := 0; c < 4; c++ {
				a0, a1, a2, a3 := t[4*c], t[4*c+1], t[4*c+2], t[4*c+3]
				t[4*c] = gfMul(a0, 14) ^ gfMul(a1, 11) ^ gfMul(a2, 13) ^ gfMul(a3, 9)
				t[4*c+1] = gfMul(a0, 9) ^ gfMul(a1, 14) ^ gfMul(a2, 11) ^ gfMul(a3, 13)
				t[4*c+2] = gfMul(a0, 13) ^ gfMul(a1, 9) ^ gfMul(a2, 14) ^ gfMul(a3, 11)
				t[4*c+3] = gfMul(a0, 11) ^ gfMul(a1, 13) ^ gfMul(a2, 9) ^ gfMul(a3, 14)
			}
		}
		s = t
	}
	copy(dst, s[:])
}

func (b roundKeyBlock) Encrypt(dst, src []byte) {
	sb := aesSbox()
	var s [16]byte
	for i := range s {
		s[i] = src[i] ^ b.rk[i]
	}
	for r := 1; r <= b.rounds; r++ {
		var t [16]byte
		for c := 0; c < 4; c++ {
			for row := 0; row < 4; row++ {
				t[4*c+row] = sb[s[4*((c+row)%4)+row]]
			}
		}
		if r < b.rounds {
			for c := 0; c < 4; c++ {
				a0, a1, a2, a3 := t[4*c], t[4*c+1], t[4*c+2], t[4*c+3]
				t[4*c] = gfMul(a0, 2) ^ gfMul(a1, 3) ^ a2 ^ a3
				t[4*c+1] = a0 ^ gfMul(a1, 2) ^ gfMul(a2, 3) ^ a3
				t[4*c+2] = a0 ^ a1 ^ gfMul(a2, 2) ^ gfMul(a3, 3)
				t[4*c+3] = gfMul(a0, 3) ^ a1 ^ a2 ^ gfMul(a3, 2)
			}
		}
		for i := range s {
			s[i] = t[i] ^ b.rk[16*r+i]
		}
	}
	copy(dst, s[:])
}

func builtinAESDecBlocks(_ *Interp, args []Value) (Value, error) {
	v, err := byteViews("__aes_dec_blocks", args, 2)
	if err != nil {
		return nil, err
	}
	rk, data := v[0], v[1]
	block, ok := newRoundKeyBlock(rk)
	if !ok {
		return nil, fmt.Errorf("__aes_dec_blocks: round keys of %d bytes", len(rk))
	}
	out := make([]byte, len(data)&^15)
	for at := 0; at < len(out); at += 16 {
		block.Decrypt(out[at:at+16], data[at:at+16])
	}
	return byteArray(out), nil
}

func builtinAESCtr32(_ *Interp, args []Value) (Value, error) {
	v, err := byteViews("__aes_ctr32", args, 3)
	if err != nil {
		return nil, err
	}
	rk, ctr, data := v[0], v[1], v[2]
	block, ok := newRoundKeyBlock(rk)
	if !ok || len(ctr) < 16 {
		return nil, fmt.Errorf("__aes_ctr32: round keys of %d bytes and a counter of %d bytes", len(rk), len(ctr))
	}
	var cb, ks [16]byte
	copy(cb[:], ctr[:16])
	out := make([]byte, len(data))
	for at := 0; at < len(data); at += 16 {
		block.Encrypt(ks[:], cb[:])
		for k := 0; k < 16 && at+k < len(data); k++ {
			out[at+k] = data[at+k] ^ ks[k]
		}
		binary.BigEndian.PutUint32(cb[12:], binary.BigEndian.Uint32(cb[12:])+1)
	}
	return byteArray(out), nil
}

func ghashMul(x, y [16]byte) [16]byte {
	var z [16]byte
	v := y
	for i := 0; i < 128; i++ {
		if x[i/8]>>(7-i%8)&1 != 0 {
			for k := range z {
				z[k] ^= v[k]
			}
		}
		lsb := v[15] & 1
		for k := 15; k > 0; k-- {
			v[k] = v[k]>>1 | v[k-1]<<7
		}
		v[0] >>= 1
		if lsb != 0 {
			v[0] ^= 0xe1
		}
	}
	return z
}

func builtinGHASH(_ *Interp, args []Value) (Value, error) {
	v, err := byteViews("__ghash", args, 3)
	if err != nil {
		return nil, err
	}
	h, y, data := v[0], v[1], v[2]
	if len(h) < 16 || len(y) < 16 {
		return nil, fmt.Errorf("__ghash: a hash key of %d bytes and a block of %d bytes", len(h), len(y))
	}
	var hb, yb [16]byte
	copy(hb[:], h)
	copy(yb[:], y)
	for at := 0; at < len(data); at += 16 {
		var blk [16]byte
		copy(blk[:], data[at:])
		for k := range yb {
			yb[k] ^= blk[k]
		}
		yb = ghashMul(yb, hb)
	}
	return byteArray(yb[:]), nil
}

// aesGCMInto seals or opens buf[at:at+n] under the nonce iv ^ seq, authenticating
// buf[aad_at:aad_at+aad_n], with the tag at buf[at+n:at+n+16], and returns the
// buffer. An open decrypts in place and leaves 0 in buf[at+n] when the tag
// verifies and 1 when it does not.
func aesGCMInto(name string, args []Value, seal bool) (Value, error) {
	if len(args) != 9 {
		return nil, fmt.Errorf("%s: expected 9 args, got %d", name, len(args))
	}
	v, err := byteViews(name, []Value{args[0], args[1], args[2], args[4]}, 4)
	if err != nil {
		return nil, err
	}
	rk, h, iv, buf := v[0], v[1], v[2], v[3]
	var ints [5]int64
	for k, a := range []Value{args[3], args[5], args[6], args[7], args[8]} {
		n, ok := a.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: argument %d is not a number", name, k)
		}
		ints[k] = int64(n)
	}
	seq, aadAt, aadN, at, n := uint64(ints[0]), ints[1], ints[2], ints[3], ints[4]
	block, ok := newRoundKeyBlock(rk)
	size := int64(len(buf))
	if !ok || len(h) < 16 || len(iv) != 12 || aadAt < 0 || aadN < 0 || at < 0 || n < 0 || at+n+16 > size || aadAt+aadN > size {
		return nil, fmt.Errorf("%s: range out of bounds", name)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	nonce := append([]byte(nil), iv...)
	binary.BigEndian.PutUint64(nonce[4:], binary.BigEndian.Uint64(nonce[4:])^seq)
	aad := buf[aadAt : aadAt+aadN]
	data := buf[at : at+n]
	tag := buf[at+n : at+n+16]
	if seal {
		sealed := gcm.Seal(nil, nonce, data, aad)
		copy(buf[at:], sealed)
		return byteArray(buf), nil
	}
	pt := make([]byte, n)
	var ctr [16]byte
	copy(ctr[:], nonce)
	binary.BigEndian.PutUint32(ctr[12:], 2)
	cipher.NewCTR(block, ctr[:]).XORKeyStream(pt, data)
	want := gcm.Seal(nil, nonce, pt, aad)[n:]
	verdict := byte(0)
	if subtle.ConstantTimeCompare(want, tag) != 1 {
		verdict = 1
	}
	copy(buf[at:], pt)
	buf[at+n] = verdict
	return byteArray(buf), nil
}

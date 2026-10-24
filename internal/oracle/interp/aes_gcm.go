package interp

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
)

func registerAESGCM(i *Interp) {
	i.Builtins["__aes_expand_key"] = &Builtin{Fn: builtinAESExpandKey}
	i.Builtins["__aes_ctr32"] = &Builtin{Fn: builtinAESCtr32}
	i.Builtins["__ghash"] = &Builtin{Fn: builtinGHASH}
	i.Builtins["__aes_gcm_seal"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return aesGCM("__aes_gcm_seal", args, true) }}
	i.Builtins["__aes_gcm_open"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) { return aesGCM("__aes_gcm_open", args, false) }}
	for _, name := range []string{"__sha256_hw", "__sha1_hw"} {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Bool(false), nil }}
	}
	for _, name := range []string{"__sha256_hw_blocks", "__sha1_hw_blocks"} {
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
		out := make([]byte, len(x.E))
		for at, e := range x.E {
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
	out := newArray(len(b))
	for at, c := range b {
		out.E[at] = Number(c)
	}
	return out
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
	if len(v[0]) != 16 && len(v[0]) != 32 {
		return newArray(0), nil
	}
	return byteArray(aesExpandKey(v[0])), nil
}

func builtinAESCtr32(_ *Interp, args []Value) (Value, error) {
	v, err := byteViews("__aes_ctr32", args, 3)
	if err != nil {
		return nil, err
	}
	rk, ctr, data := v[0], v[1], v[2]
	keyLen := 0
	switch {
	case len(rk) >= 240:
		keyLen = 32
	case len(rk) >= 176:
		keyLen = 16
	}
	if keyLen == 0 || len(ctr) < 16 {
		return newArray(0), nil
	}
	block, err := aes.NewCipher(rk[:keyLen])
	if err != nil {
		return nil, fmt.Errorf("__aes_ctr32: %v", err)
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
		return newArray(0), nil
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

func aesGCM(name string, args []Value, seal bool) (Value, error) {
	v, err := byteViews(name, args, 5)
	if err != nil {
		return nil, err
	}
	rk, h, nonce, aad, data := v[0], v[1], v[2], v[3], v[4]
	keyLen := 0
	switch {
	case len(rk) >= 240:
		keyLen = 32
	case len(rk) >= 176:
		keyLen = 16
	}
	if keyLen == 0 || len(h) < 16 || len(nonce) != 12 {
		return newArray(0), nil
	}
	block, err := aes.NewCipher(rk[:keyLen])
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	if seal {
		return byteArray(gcm.Seal(nil, nonce, data, aad)), nil
	}
	pt := make([]byte, len(data))
	var ctr [16]byte
	copy(ctr[:], nonce)
	binary.BigEndian.PutUint32(ctr[12:], 2)
	cipher.NewCTR(block, ctr[:]).XORKeyStream(pt, data)
	sealed := gcm.Seal(nil, nonce, pt, aad)
	return byteArray(append(pt, sealed[len(pt):]...)), nil
}

package interp

import (
	"fmt"
	"math/bits"
)

func registerMontMul(i *Interp) {
	i.Builtins["__mont_mul"] = &Builtin{Fn: builtinMontMul}
}

// MontMul is the contract of __mont_mul: one Montgomery product of a and b
// by coarsely integrated operand scanning (CIOS). m[0] is n0 = -p^-1 mod 2^64
// and m[1:] holds the n limbs of p, little-endian. The result is the CIOS sum
// a b R^-1 mod p in n limbs, less p when that is not negative. a and b have n
// limbs each.
func MontMul(a, b, m []uint64) []uint64 {
	n0, p := m[0], m[1:]
	n := len(p)
	t := make([]uint64, n)
	var top uint64
	for i := range n {
		hi, lo := bits.Mul64(a[i], b[0])
		s, c := bits.Add64(t[0], lo, 0)
		c1 := hi + c
		q := s * n0
		hi, lo = bits.Mul64(q, p[0])
		_, c = bits.Add64(s, lo, 0)
		c2 := hi + c
		for j := 1; j < n; j++ {
			hi, lo = bits.Mul64(a[i], b[j])
			u, c := bits.Add64(t[j], lo, 0)
			hi += c
			u, c = bits.Add64(u, c1, 0)
			c1 = hi + c
			hi, lo = bits.Mul64(q, p[j])
			u, c = bits.Add64(u, lo, 0)
			hi += c
			u, c = bits.Add64(u, c2, 0)
			c2 = hi + c
			t[j-1] = u
		}
		u, e1 := bits.Add64(top, c1, 0)
		u, e2 := bits.Add64(u, c2, 0)
		t[n-1] = u
		top = e1 + e2
	}
	var borrow uint64
	for j := range n {
		_, borrow = bits.Sub64(t[j], p[j], borrow)
	}
	mask := -((borrow ^ 1) | top)
	borrow = 0
	for j := range n {
		t[j], borrow = bits.Sub64(t[j], p[j]&mask, borrow)
	}
	return t
}

// limbView reads a [i64] argument as unsigned limbs.
func limbView(name string, v Value) ([]uint64, error) {
	x, ok := v.(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected an [i64], got %T", name, v)
	}
	out := make([]uint64, len(x.Elems()))
	for at, e := range x.Elems() {
		w, ok := e.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: element %d is not i64", name, at)
		}
		out[at] = uint64(w)
	}
	return out, nil
}

func builtinMontMul(_ *Interp, args []Value) (Value, error) {
	const name = "__mont_mul"
	if len(args) != 3 {
		return nil, fmt.Errorf("%s: expected a, b and m, got %d args", name, len(args))
	}
	var v [3][]uint64
	for at, arg := range args {
		l, err := limbView(name, arg)
		if err != nil {
			return nil, err
		}
		v[at] = l
	}
	n := len(v[2]) - 1
	if n < 1 || len(v[0]) != n || len(v[1]) != n {
		return nil, fmt.Errorf("%s: the limbs of a and b do not match the modulus", name)
	}
	t := MontMul(v[0], v[1], v[2])
	out := newArray(n)
	for at, w := range t {
		out.Elems()[at] = Number(int64(w))
	}
	return out, nil
}

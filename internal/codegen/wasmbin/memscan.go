package wasmbin

import "fmt"

// Deciding whether a module needs a memory section from the bytes the
// emitter just produced, rather than from a list of the constructs that
// were known to need one when the list was written.
//
// The walk below is strict: an opcode whose immediate shape it does not
// know is an error, not a guess. That is what makes the answer derived —
// a new instruction in the emitter either has a shape here or fails the
// build loudly, where a stale predicate would have emitted a module the
// validator rejects with "unknown memory 0".

// codeUsesMemory reports whether one code-section entry — the
// size-prefixed locals vector plus expression that inst.PutFunctionBody
// produces — contains an instruction that addresses memory 0.
func codeUsesMemory(code []byte) (bool, error) {
	r := &codeReader{b: code}
	size, err := r.uleb()
	if err != nil {
		return false, fmt.Errorf("body size: %w", err)
	}
	if size > uint64(len(code)-r.i) {
		return false, fmt.Errorf("body size %d overruns %d remaining bytes", size, len(code)-r.i)
	}
	end := r.i + int(size)
	groups, err := r.uleb()
	if err != nil {
		return false, fmt.Errorf("locals vector: %w", err)
	}
	for g := uint64(0); g < groups; g++ {
		if _, err := r.uleb(); err != nil {
			return false, fmt.Errorf("locals group %d count: %w", g, err)
		}
		if err := r.skip(1); err != nil {
			return false, fmt.Errorf("locals group %d valtype: %w", g, err)
		}
	}
	used := false
	for r.i < end {
		at := r.i
		mem, err := r.instruction()
		if err != nil {
			return false, fmt.Errorf("at body offset %d: %w", at, err)
		}
		used = used || mem
	}
	if r.i != end {
		return false, fmt.Errorf("instruction sequence overruns the body by %d bytes", r.i-end)
	}
	return used, nil
}

// codeReader is a cursor over one encoded function body.
type codeReader struct {
	b []byte
	i int
}

func (r *codeReader) next() (byte, error) {
	if r.i >= len(r.b) {
		return 0, fmt.Errorf("unexpected end of body")
	}
	b := r.b[r.i]
	r.i++
	return b, nil
}

func (r *codeReader) skip(n int) error {
	if r.i+n > len(r.b) {
		return fmt.Errorf("unexpected end of body")
	}
	r.i += n
	return nil
}

func (r *codeReader) uleb() (uint64, error) {
	var v uint64
	var shift uint
	for {
		b, err := r.next()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, fmt.Errorf("uleb128 too long")
		}
	}
}

func (r *codeReader) sleb() error {
	for {
		b, err := r.next()
		if err != nil {
			return err
		}
		if b&0x80 == 0 {
			return nil
		}
	}
}

// memarg is the (align, offset) pair every load / store carries.
func (r *codeReader) memarg() error {
	if _, err := r.uleb(); err != nil {
		return err
	}
	_, err := r.uleb()
	return err
}

// blocktype is either a one-byte form (0x40 for void, or a valtype) or a
// signed-leb typeidx.
func (r *codeReader) blocktype() error {
	b, err := r.next()
	if err != nil {
		return err
	}
	switch b {
	case 0x40, // void
		0x7f, 0x7e, 0x7d, 0x7c, // i32 i64 f32 f64
		0x7b,       // v128
		0x70, 0x6f: // funcref externref
		return nil
	}
	r.i-- // a typeidx, signed-leb encoded
	return r.sleb()
}

// instruction consumes one instruction and reports whether it addresses
// memory 0.
func (r *codeReader) instruction() (bool, error) {
	op, err := r.next()
	if err != nil {
		return false, err
	}
	switch {
	case op == 0x00 || op == 0x01: // unreachable, nop
		return false, nil
	case op >= 0x02 && op <= 0x04: // block, loop, if
		return false, r.blocktype()
	case op == 0x05 || op == 0x0b || op == 0x0f: // else, end, return
		return false, nil
	case op == 0x0c || op == 0x0d: // br, br_if
		_, err := r.uleb()
		return false, err
	case op == 0x0e: // br_table: vec(labelidx) + default
		n, err := r.uleb()
		if err != nil {
			return false, err
		}
		for k := uint64(0); k <= n; k++ {
			if _, err := r.uleb(); err != nil {
				return false, err
			}
		}
		return false, nil
	case op == 0x10: // call
		_, err := r.uleb()
		return false, err
	case op == 0x11: // call_indirect: typeidx + tableidx
		if _, err := r.uleb(); err != nil {
			return false, err
		}
		_, err := r.uleb()
		return false, err
	case op == 0x1a || op == 0x1b: // drop, select
		return false, nil
	case op == 0x1c: // select with a result-type vector
		n, err := r.uleb()
		if err != nil {
			return false, err
		}
		return false, r.skip(int(n))
	case op >= 0x20 && op <= 0x26: // local.*, global.*, table.get, table.set
		_, err := r.uleb()
		return false, err
	case op >= 0x28 && op <= 0x3e: // every load and store
		return true, r.memarg()
	case op == 0x3f || op == 0x40: // memory.size, memory.grow
		return true, r.skip(1) // reserved memidx
	case op == 0x41: // i32.const
		return false, r.sleb()
	case op == 0x42: // i64.const
		return false, r.sleb()
	case op == 0x43: // f32.const
		return false, r.skip(4)
	case op == 0x44: // f64.const
		return false, r.skip(8)
	case op >= 0x45 && op <= 0xc4: // comparison, arithmetic, conversion, sign-extend
		return false, nil
	case op == 0xd0: // ref.null
		return false, r.skip(1)
	case op == 0xd1: // ref.is_null
		return false, nil
	case op == 0xd2: // ref.func
		_, err := r.uleb()
		return false, err
	case op == 0xfc:
		return r.prefixedFC()
	case op == 0xfd:
		return r.prefixedFD()
	}
	return false, fmt.Errorf("unknown opcode 0x%02x", op)
}

// prefixedFC handles the 0xFC family: saturating truncations and bulk
// memory / table operations.
func (r *codeReader) prefixedFC() (bool, error) {
	sub, err := r.uleb()
	if err != nil {
		return false, err
	}
	switch {
	case sub <= 7: // i32/i64 trunc_sat_f32/f64_s/u
		return false, nil
	case sub == 8: // memory.init: dataidx + reserved memidx
		if _, err := r.uleb(); err != nil {
			return false, err
		}
		return true, r.skip(1)
	case sub == 9: // data.drop
		_, err := r.uleb()
		return false, err
	case sub == 10: // memory.copy: two reserved memidx bytes
		return true, r.skip(2)
	case sub == 11: // memory.fill: one reserved memidx byte
		return true, r.skip(1)
	case sub == 12 || sub == 14: // table.init, table.copy: two indices
		if _, err := r.uleb(); err != nil {
			return false, err
		}
		_, err := r.uleb()
		return false, err
	case sub == 13 || (sub >= 15 && sub <= 17): // elem.drop, table.grow/size/fill
		_, err := r.uleb()
		return false, err
	}
	return false, fmt.Errorf("unknown 0xfc sub-opcode %d", sub)
}

// prefixedFD handles the 0xFD (vector) family. Only the load / store
// forms address memory; the lane-immediate and constant forms carry
// fixed-size immediates that still have to be stepped over.
func (r *codeReader) prefixedFD() (bool, error) {
	sub, err := r.uleb()
	if err != nil {
		return false, err
	}
	switch {
	case sub <= 11: // v128.load*, v128.store
		return true, r.memarg()
	case sub == 12: // v128.const
		return false, r.skip(16)
	case sub == 13: // i8x16.shuffle: 16 lane indices
		return false, r.skip(16)
	case sub >= 21 && sub <= 34: // extract_lane / replace_lane
		return false, r.skip(1)
	case sub >= 84 && sub <= 91: // v128.loadN_lane / v128.storeN_lane
		if err := r.memarg(); err != nil {
			return false, err
		}
		return true, r.skip(1)
	case sub == 92 || sub == 93: // v128.load32_zero, v128.load64_zero
		return true, r.memarg()
	case sub <= 275: // the remaining vector ops take no immediate
		return false, nil
	}
	return false, fmt.Errorf("unknown 0xfd sub-opcode %d", sub)
}

package interp

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// rawMemory is the byte arena behind __alloc. A value's own memory has no
// address here, so a load or store must fall inside a live __alloc block.
type rawMemory struct {
	bytes  []byte
	blocks []rawBlock
	free   map[int64][]int
}

type rawBlock struct {
	off, size int64
	live      bool
}

// rawBase keeps the null address and the small integers out of the arena.
const (
	rawBase  = 0x10000
	rawAlign = 16
)

func rawSlot(n int64) int64 {
	return max(rawAlign, (n+rawAlign-1)&^(rawAlign-1))
}

func (m *rawMemory) alloc(n int64) int64 {
	slot := rawSlot(n)
	if m.free == nil {
		m.free = map[int64][]int{}
	}
	if idxs := m.free[slot]; len(idxs) > 0 {
		k := idxs[len(idxs)-1]
		m.free[slot] = idxs[:len(idxs)-1]
		b := &m.blocks[k]
		b.size, b.live = n, true
		clear(m.bytes[b.off : b.off+slot])
		return rawBase + b.off
	}
	off := int64(len(m.bytes))
	m.bytes = append(m.bytes, make([]byte, slot)...)
	m.blocks = append(m.blocks, rawBlock{off: off, size: n, live: true})
	return rawBase + off
}

func (m *rawMemory) block(addr int64) int {
	off := addr - rawBase
	if off < 0 || off >= int64(len(m.bytes)) {
		return -1
	}
	return sort.Search(len(m.blocks), func(k int) bool { return m.blocks[k].off > off }) - 1
}

func (m *rawMemory) release(name string, addr, n int64) error {
	k := m.block(addr)
	if k < 0 || m.blocks[k].off != addr-rawBase {
		return fmt.Errorf("%s: address %#x is not the start of a block from __alloc", name, uint64(addr))
	}
	b := &m.blocks[k]
	if !b.live {
		return fmt.Errorf("%s: the block at %#x is already free", name, uint64(addr))
	}
	if b.size != n {
		return fmt.Errorf("%s: the block at %#x has %d bytes, not %d", name, uint64(addr), b.size, n)
	}
	b.live = false
	slot := rawSlot(n)
	m.free[slot] = append(m.free[slot], k)
	return nil
}

// span returns the n bytes at addr, which must lie inside one live block.
func (m *rawMemory) span(name string, addr, n int64) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("%s: length %d is below zero", name, n)
	}
	if n == 0 {
		return nil, nil
	}
	k := m.block(addr)
	if k < 0 {
		return nil, fmt.Errorf("%s: address %#x is not in a block from __alloc; the interpreter has no layout for a value's own memory", name, uint64(addr))
	}
	b := m.blocks[k]
	if !b.live {
		return nil, fmt.Errorf("%s: address %#x is in a freed block", name, uint64(addr))
	}
	off := addr - rawBase
	if off+n > b.off+b.size {
		return nil, fmt.Errorf("%s: %d bytes at %#x run past the end of the %d-byte block at %#x", name, n, uint64(addr), b.size, uint64(rawBase+b.off))
	}
	return m.bytes[off : off+n], nil
}

func rawArgs(name string, args []Value, want int) ([]int64, error) {
	if len(args) != want {
		return nil, fmt.Errorf("%s: expected %d args, got %d", name, want, len(args))
	}
	out := make([]int64, want)
	for k, a := range args {
		n, ok := a.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: arg %d must be a number, got %T", name, k+1, a)
		}
		out[k] = int64(n)
	}
	return out, nil
}

func builtinRawAlloc(ip *Interp, args []Value) (Value, error) {
	a, err := rawArgs("__alloc", args, 1)
	if err != nil {
		return nil, err
	}
	if a[0] < 0 {
		return nil, fmt.Errorf("__alloc: size %d is below zero", a[0])
	}
	return Number(ip.raw.alloc(a[0])), nil
}

func builtinRawFree(ip *Interp, args []Value) (Value, error) {
	a, err := rawArgs("__free", args, 2)
	if err != nil {
		return nil, err
	}
	return Void{}, ip.raw.release("__free", a[0], a[1])
}

func builtinRawMemcpy(ip *Interp, args []Value) (Value, error) {
	a, err := rawArgs("__memcpy", args, 3)
	if err != nil {
		return nil, err
	}
	dst, err := ip.raw.span("__memcpy", a[0], a[2])
	if err != nil {
		return nil, err
	}
	src, err := ip.raw.span("__memcpy", a[1], a[2])
	if err != nil {
		return nil, err
	}
	if a[2] > 0 && a[0] < a[1]+a[2] && a[1] < a[0]+a[2] {
		return nil, fmt.Errorf("__memcpy: the %d bytes at %#x and at %#x overlap", a[2], uint64(a[0]), uint64(a[1]))
	}
	copy(dst, src)
	return Void{}, nil
}

func builtinRawMemset(ip *Interp, args []Value) (Value, error) {
	a, err := rawArgs("__memset", args, 3)
	if err != nil {
		return nil, err
	}
	dst, err := ip.raw.span("__memset", a[0], a[2])
	if err != nil {
		return nil, err
	}
	for k := range dst {
		dst[k] = byte(a[1])
	}
	return Void{}, nil
}

func rawLoad(name string, width int64, decode func([]byte) int64) func(*Interp, []Value) (Value, error) {
	return func(ip *Interp, args []Value) (Value, error) {
		a, err := rawArgs(name, args, 1)
		if err != nil {
			return nil, err
		}
		b, err := ip.raw.span(name, a[0], width)
		if err != nil {
			return nil, err
		}
		return Number(decode(b)), nil
	}
}

func rawStore(name string, width int64, encode func([]byte, int64)) func(*Interp, []Value) (Value, error) {
	return func(ip *Interp, args []Value) (Value, error) {
		a, err := rawArgs(name, args, 2)
		if err != nil {
			return nil, err
		}
		b, err := ip.raw.span(name, a[0], width)
		if err != nil {
			return nil, err
		}
		encode(b, a[1])
		return Void{}, nil
	}
}

func registerRawMemory(i *Interp) {
	le := binary.LittleEndian
	ptrBytes := int64(hostPtrBits / 8)
	loadPtr := func(b []byte) int64 { return int64(le.Uint64(b)) }
	storePtr := func(b []byte, v int64) { le.PutUint64(b, uint64(v)) }
	if ptrBytes == 4 {
		loadPtr = func(b []byte) int64 { return int64(le.Uint32(b)) }
		storePtr = func(b []byte, v int64) { le.PutUint32(b, uint32(v)) }
	}
	i.Builtins["__alloc"] = &Builtin{Fn: builtinRawAlloc}
	i.Builtins["__free"] = &Builtin{Fn: builtinRawFree}
	i.Builtins["__memcpy"] = &Builtin{Fn: builtinRawMemcpy}
	i.Builtins["__memset"] = &Builtin{Fn: builtinRawMemset}
	i.Builtins["__load_u8"] = &Builtin{Fn: rawLoad("__load_u8", 1, func(b []byte) int64 { return int64(b[0]) })}
	i.Builtins["__load_i32"] = &Builtin{Fn: rawLoad("__load_i32", 4, func(b []byte) int64 { return int64(int32(le.Uint32(b))) })}
	i.Builtins["__load_i64"] = &Builtin{Fn: rawLoad("__load_i64", 8, func(b []byte) int64 { return int64(le.Uint64(b)) })}
	i.Builtins["__load_ptr"] = &Builtin{Fn: rawLoad("__load_ptr", ptrBytes, loadPtr)}
	i.Builtins["__store_u8"] = &Builtin{Fn: rawStore("__store_u8", 1, func(b []byte, v int64) { b[0] = byte(v) })}
	i.Builtins["__store_i32"] = &Builtin{Fn: rawStore("__store_i32", 4, func(b []byte, v int64) { le.PutUint32(b, uint32(v)) })}
	i.Builtins["__store_i64"] = &Builtin{Fn: rawStore("__store_i64", 8, func(b []byte, v int64) { le.PutUint64(b, uint64(v)) })}
	i.Builtins["__store_ptr"] = &Builtin{Fn: rawStore("__store_ptr", ptrBytes, storePtr)}
	i.Builtins["__ptr_width"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if _, err := rawArgs("__ptr_width", args, 0); err != nil {
			return nil, err
		}
		return Number(ptrBytes), nil
	}}
}

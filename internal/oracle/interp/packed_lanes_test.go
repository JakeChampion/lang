package interp

import (
	"fmt"
	"testing"
)

func TestPackedLanesPreserveBitsAndAliases(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	values := make([]Value, 33)
	for k := range values {
		values[k] = Number(uint8(k*37 + 11))
	}
	source := arrayOf(values...)
	for _, width := range []int{32, 64} {
		prefix := fmt.Sprintf("__f%dx%d_", width, 128/width)
		got, err := i.Builtins[prefix+"load_bytes"].Fn(i, []Value{source, Number(1)})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := i.Builtins[prefix+"with_bytes"].Fn(i, []Value{source, Number(17), got})
		if err != nil {
			t.Fatal(err)
		}
		out := stored.(Array)
		for k := 0; k < 16; k++ {
			if out.Elems()[17+k] != values[1+k] {
				t.Fatalf("width %d byte %d: got %v", width, k, out.Elems()[17+k])
			}
			if source.Elems()[17+k] != values[17+k] {
				t.Fatalf("width %d changed an alias", width)
			}
		}
		for _, at := range []Number{-1, 18, 2147483647} {
			if _, err := i.Builtins[prefix+"load_bytes"].Fn(i, []Value{source, at}); err == nil {
				t.Fatalf("width %d accepted offset %d", width, at)
			}
			if _, err := i.Builtins[prefix+"with_bytes"].Fn(i, []Value{source, at, got}); err == nil {
				t.Fatalf("width %d store accepted offset %d", width, at)
			}
		}
	}
}

func TestRawLaneMemoryChecksAllocationBounds(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	registerRawMemory(i)
	for _, width := range []int{32, 64} {
		prefix := fmt.Sprintf("__f%dx%d_", width, 128/width)
		address, err := i.Builtins["__alloc"].Fn(i, []Value{Number(33)})
		if err != nil {
			t.Fatal(err)
		}
		p := address.(Number)
		lanes := Lanes{Width: width, Bits: make([]uint64, 128/width)}
		for k := range lanes.Bits {
			lanes.Bits[k] = uint64(0xfff123456789abcd) + uint64(k)
		}
		if _, err := i.Builtins[prefix+"store"].Fn(i, []Value{p + 1, lanes}); err != nil {
			t.Fatal(err)
		}
		loaded, err := i.Builtins[prefix+"load_ptr"].Fn(i, []Value{p + 1})
		if err != nil {
			t.Fatal(err)
		}
		got := loaded.(Lanes)
		for k, bits := range got.Bits {
			want := lanes.Bits[k]
			if width == 32 {
				want = uint64(uint32(want))
			}
			if bits != want {
				t.Fatalf("width %d lane %d: %x, want %x", width, k, bits, want)
			}
		}
		if _, err := i.Builtins[prefix+"load_ptr"].Fn(i, []Value{p + 18}); err == nil {
			t.Fatal("accepted an out-of-bounds load")
		}
		bytes, err := i.raw.span("test", int64(p+1), 16)
		if err != nil {
			t.Fatal(err)
		}
		for k, b := range bytes {
			want := byte(lanes.Bits[k/(width/8)] >> ((k % (width / 8)) * 8))
			if b != want {
				t.Fatalf("width %d byte %d: %x, want %x", width, k, b, want)
			}
		}
		if _, err := i.Builtins[prefix+"store"].Fn(i, []Value{p + 18, lanes}); err == nil {
			t.Fatal("accepted a store beyond the allocation")
		}
		if _, err := i.Builtins["__free"].Fn(i, []Value{p, Number(33)}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.Builtins[prefix+"load_ptr"].Fn(i, []Value{p}); err == nil {
			t.Fatal("accepted a load after release")
		}
		if _, err := i.Builtins[prefix+"store"].Fn(i, []Value{p, lanes}); err == nil {
			t.Fatal("accepted a store after release")
		}
	}
}

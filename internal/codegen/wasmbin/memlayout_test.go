package wasmbin

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
)

func TestMemoryMinPages(t *testing.T) {
	cases := []struct {
		name    string
		offsets []int32
		inits   [][]byte
		want    uint32
	}{
		{"no data at all", nil, nil, 1},
		{"a few bytes low down", []int32{40}, [][]byte{make([]byte, 4)}, 1},
		{"exactly one page", []int32{0}, [][]byte{make([]byte, 65536)}, 1},
		{"one byte past a page", []int32{0}, [][]byte{make([]byte, 65537)}, 2},
		{"offset alone crosses the page", []int32{65530}, [][]byte{make([]byte, 8)}, 2},
		{"the extent is the max, not the last", []int32{200000, 40}, [][]byte{make([]byte, 16), make([]byte, 4)}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryMinPages(tc.offsets, tc.inits); got != tc.want {
				t.Errorf("memoryMinPages = %d pages, want %d", got, tc.want)
			}
		})
	}
}

// The emitted module's initial memory must cover every data segment it
// declares. Data segments are written at INSTANTIATION, so a module whose
// literals reach past the initial size traps before `main` runs — the
// module cannot be started at all, whatever its code does. Pinning the
// property on the emitted bytes catches a regression the end-to-end test
// would only see for the literal sizes it happens to use.
func TestEmittedMemoryCoversStaticData(t *testing.T) {
	// One literal well past a page, so the memory section has to grow
	// beyond the one page the emitter used to hardcode.
	long := strings.Repeat("q", 200000)
	prog := &ir.Program{Funcs: []*ir.Func{{
		Name:       "main",
		ReturnType: i32(),
		Ops: []ir.Op{
			{Kind: ir.OpConstStr, Str: long},
			{Kind: ir.OpStrLen},
		},
	}}}
	bin, err := Emit(prog)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	minPages, ok := emittedMemoryMin(t, bin)
	if !ok {
		t.Fatalf("no memory section in a module with a string literal")
	}
	extent := emittedDataExtent(t, bin)
	if extent <= wasmPageSize {
		t.Fatalf("static data extent is %d bytes — the case no longer reaches past one page, so it proves nothing", extent)
	}
	if int(minPages)*wasmPageSize < extent {
		t.Errorf("initial memory is %d page(s) = %d bytes, but the data segments reach %d — the module traps on instantiation",
			minPages, int(minPages)*wasmPageSize, extent)
	}
}

// emittedMemoryMin decodes the min-pages field of the module's memory
// section, reporting whether one is present.
func emittedMemoryMin(t *testing.T, bin []byte) (uint32, bool) {
	t.Helper()
	body, ok := sectionBody(t, bin, encode.SectionMemory)
	if !ok {
		return 0, false
	}
	p := 0
	if count := readUleb(t, body, &p); count != 1 {
		t.Fatalf("memory section declares %d memories, want 1", count)
	}
	p++ // limits flags
	return readUleb(t, body, &p), true
}

// emittedDataExtent decodes the module's active data segments and returns
// the first address past the highest one.
func emittedDataExtent(t *testing.T, bin []byte) int {
	t.Helper()
	body, ok := sectionBody(t, bin, encode.SectionData)
	if !ok {
		return 0
	}
	p := 0
	count := readUleb(t, body, &p)
	end := 0
	for i := uint32(0); i < count; i++ {
		if mode := readUleb(t, body, &p); mode != 0 {
			t.Fatalf("data segment %d is mode %d, want 0 (active, memory 0)", i, mode)
		}
		if body[p] != 0x41 {
			t.Fatalf("data segment %d offset expr starts with 0x%02x, want i32.const", i, body[p])
		}
		p++
		off := readSleb(t, body, &p)
		if body[p] != 0x0b {
			t.Fatalf("data segment %d offset expr does not end", i)
		}
		p++
		n := int(readUleb(t, body, &p))
		if e := int(off) + n; e > end {
			end = e
		}
		p += n
	}
	return end
}

// sectionBody returns the payload of the first section with the given id.
func sectionBody(t *testing.T, bin []byte, want byte) ([]byte, bool) {
	t.Helper()
	if len(bin) < 8 {
		t.Fatalf("module is %d bytes — no preamble", len(bin))
	}
	p := 8
	for p < len(bin) {
		id := bin[p]
		p++
		size := int(readUleb(t, bin, &p))
		if id == want {
			return bin[p : p+size], true
		}
		p += size
	}
	return nil, false
}

func readUleb(t *testing.T, b []byte, p *int) uint32 {
	t.Helper()
	var v uint32
	var shift uint
	for {
		if *p >= len(b) {
			t.Fatalf("truncated uleb128")
		}
		c := b[*p]
		*p++
		v |= uint32(c&0x7f) << shift
		if c&0x80 == 0 {
			return v
		}
		shift += 7
	}
}

func readSleb(t *testing.T, b []byte, p *int) int32 {
	t.Helper()
	var v int32
	var shift uint
	for {
		if *p >= len(b) {
			t.Fatalf("truncated sleb128")
		}
		c := b[*p]
		*p++
		v |= int32(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 {
			if shift < 32 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return v
		}
	}
}

package interp

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestLaneWidenLoadConversionAndBounds(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	registerRawMemory(i)
	address, err := i.Builtins["__alloc"].Fn(i, []Value{Number(17)})
	if err != nil {
		t.Fatal(err)
	}
	p := address.(Number)
	bytes, err := i.raw.span("test", int64(p), 17)
	if err != nil {
		t.Fatal(err)
	}
	load := i.Builtins["__f64x2_load_f32_ptr"]
	seed := uint32(417)
	edges := []uint32{0, 1, 0x80000000, 0x80000001, 0x7fffff, 0x800000, 0x7f7fffff, 0xff7fffff, 0x7f800000, 0xff800000, 0x7f800001, 0x7fc45678}
	for trial := 0; trial < 1024; trial++ {
		bytes[0] = 0xa5
		for lane := 0; lane < 4; lane++ {
			seed = seed*1664525 + 1013904223
			word := seed
			if trial < len(edges) {
				word = edges[(trial+lane)%len(edges)]
			}
			binary.LittleEndian.PutUint32(bytes[1+4*lane:], word)
		}
		before := append([]byte(nil), bytes...)
		for pair := 0; pair < 2; pair++ {
			value, err := load.Fn(i, []Value{p + Number(1+8*pair)})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(Lanes)
			if got.Width != 64 || len(got.Bits) != 2 {
				t.Fatalf("unexpected lanes: %v", got)
			}
			for lane := 0; lane < 2; lane++ {
				word := binary.LittleEndian.Uint32(before[1+8*pair+4*lane:])
				want := float64(math.Float32frombits(word))
				if math.IsNaN(want) {
					if !math.IsNaN(math.Float64frombits(got.Bits[lane])) {
						t.Fatal("NaN was lost")
					}
				} else if got.Bits[lane] != math.Float64bits(want) {
					t.Fatalf("word %x: got %x, want %x", word, got.Bits[lane], math.Float64bits(want))
				}
			}
		}
		for at, b := range bytes {
			if b != before[at] {
				t.Fatal("load changed its source")
			}
		}
	}
	if _, err := load.Fn(i, []Value{p + 10}); err == nil {
		t.Fatal("accepted a load beyond the eight-byte span")
	}
	if _, err := i.Builtins["__free"].Fn(i, []Value{p, Number(17)}); err != nil {
		t.Fatal(err)
	}
	if _, err := load.Fn(i, []Value{p + 1}); err == nil {
		t.Fatal("accepted a load after release")
	}
}

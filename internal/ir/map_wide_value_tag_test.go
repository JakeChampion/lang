package ir

import "testing"

// mapNewValTag returns the valTag const `fn` pushes as map_new's last
// argument — the word map_new_impl stores at buf+12.
func mapNewValTag(t *testing.T, p *Program, fnName string) int32 {
	t.Helper()
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for i, op := range fn.Ops {
			if isNamedCallKind(op.Kind) && op.Str == "map_new" {
				return fn.Ops[i-1].I32
			}
		}
	}
	t.Fatalf("no map_new call in %s:\n%s", fnName, p)
	return 0
}

// TestMapValTagCarriesBoxedCellSize pins the size the runtime frees a
// displaced wide-scalar value cell by (#7114). The value column of a
// Map[K, i64 / u64 / f64] holds pointers to 8-byte cells the column owns;
// __map_val_cell_bytes reads that size out of the tag's high bytes, and a
// zero there means the slots hold their values directly.
func TestMapValTagCarriesBoxedCellSize(t *testing.T) {
	cases := []struct {
		name  string
		vType string
		ptrW  int
		want  int32
	}{
		{"i64 native", "i64", 8, 0 | 8<<8},
		{"i64 wasm32", "i64", 4, 0 | 8<<8},
		{"f64 native", "f64", 8, 0 | 8<<8},
		{"f64 wasm32", "f64", 4, 0 | 8<<8},
		{"i32 native", "i32", 8, 0},
		{"i32 wasm32", "i32", 4, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := lowerSourceWith(t, `function build(): i32 {
    var m: Map[i32, `+c.vType+`] = map_new(8);
    return 0;
}`, c.ptrW)
			if got := mapNewValTag(t, p, "build"); got != c.want {
				t.Fatalf("Map[i32, %s] valTag = %d, want %d", c.vType, got, c.want)
			}
		})
	}
}

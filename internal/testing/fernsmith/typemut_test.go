package fernsmith

import (
	"strings"
	"testing"
)

func TestLetAnnotationSpans(t *testing.T) {
	src := "let a: i32 = 1i32; let f: (i32) => i32 = g; let m: Map[i32, i32] = {}; x = 1;"
	var got []string
	for _, sp := range letAnnotationSpans(src) {
		got = append(got, src[sp[0]:sp[1]])
	}
	want := []string{"i32", "(i32) => i32", "Map[i32, i32]"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("spans = %q, want %q", got, want)
	}
}

func TestTypeMutants(t *testing.T) {
	withMutants := 0
	for seed := uint64(0); seed < 64; seed++ {
		src := GenMain(seed)
		muts := TypeMutants(src, seed)
		if len(muts) > 0 {
			withMutants++
		}
		for _, m := range muts {
			if m.Src == src {
				t.Fatalf("seed %d: a mutant is the original program", seed)
			}
			if m.Erased {
				if len(m.Src) != len(src)-len(": "+m.Ann) {
					t.Fatalf("seed %d: erasing %q removed %d bytes", seed, m.Ann, len(src)-len(m.Src))
				}
			} else if len(m.Src) != len(src)-len(m.Ann)+len(m.Repl) {
				t.Fatalf("seed %d: replacing %q with %q changed the length by %d", seed, m.Ann, m.Repl, len(m.Src)-len(src))
			}
		}
	}
	if withMutants < 60 {
		t.Errorf("only %d of 64 programs have a `let` annotation to mutate", withMutants)
	}
}

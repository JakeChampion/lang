package interp

import "testing"

// A struct pattern at a payload slot projects the struct's fields. The
// trailing `..` binds nothing, and a rename projects a field into a
// differently-named local.
func TestStructPatternAtPayloadSlotBinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  string
		want int64
	}{
		{"rest", `S(Pt { x, .. }) => { return x; }`, 7},
		{"all fields", `S(Pt { x, y, z }) => { return x + y + z; }`, 10},
		{"rename", `S(Pt { x: a, .. }) => { return a; }`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `struct Pt { x: i32, y: i32, z: i32 }
				enum H { S(Pt), N(i32) }
				function f(h: H): i32 {
					match (h) {
						` + tc.arm + `,
						N(n) => { return n; }
					}
				}
				function main(): i32 { return f(S(Pt { x: 7, y: 1, z: 2 })); }`
			got := evalProgramValue(t, src)
			if n, ok := got.(Number); !ok || int64(n) != tc.want {
				t.Errorf("got %v, want %d", got, tc.want)
			}
		})
	}
}

// A field literal makes the slot refutable, so a struct slot falls to the
// NEXT ARM exactly as a variant slot does — the property that makes flat
// arms a pattern matrix rather than a merge.
func TestStructPatternAtPayloadSlotFallsToNextArm(t *testing.T) {
	src := `struct Pt { x: i32, y: i32 }
		enum H { S(Pt), N(i32) }
		function f(h: H): i32 {
			match (h) {
				S(Pt { x: 1, y }) => { return 1000 + y; },
				S(Pt { x, .. }) => { return x; },
				N(n) => { return n; }
			}
		}
		function main(): i32 { return f(S(Pt { x: 4, y: 5 })); }`
	got := evalProgramValue(t, src)
	if n, ok := got.(Number); !ok || int64(n) != 4 {
		t.Errorf("a failing field literal must fall to the next arm: got %v, want 4", got)
	}
}

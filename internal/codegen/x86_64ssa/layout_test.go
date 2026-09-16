package x86_64ssa

import "testing"

// prog builds a Program from terminators alone; the layout only reads those.
func prog(entry int, terms ...Term) *Program {
	p := &Program{Entry: entry}
	for _, t := range terms {
		p.Blocks = append(p.Blocks, MBlock{Term: t})
	}
	return p
}

// The emitter numbers blocks in the lifter's creation order and
// appends critical-edge splits at the end, so index order leaves a branch in
// front of nearly every label. The walk follows the fallthrough chain instead.
func TestLayoutOrderFollowsTheFallthroughChain(t *testing.T) {
	// b0 -> b3 -> b1; b2 is the taken arm of b0 and is placed after the chain.
	p := prog(0,
		Term{Kind: TBrIf, True: 2, False: 3},
		Term{Kind: TRet},
		Term{Kind: TJmp, Target: 1},
		Term{Kind: TJmp, Target: 1},
	)
	got := LayoutOrder(p)
	want := []int{0, 3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// Every block must be written exactly once whatever the CFG shape, or a label
// dangles (missing) or is defined twice (duplicate).
func TestLayoutOrderIsAPermutation(t *testing.T) {
	cases := map[string]*Program{
		"self loop": prog(0,
			Term{Kind: TBrIf, True: 0, False: 1},
			Term{Kind: TRet},
		),
		"unreferenced block": prog(0,
			Term{Kind: TRet},
			Term{Kind: TJmp, Target: 0},
		),
		"entry is not block 0": prog(2,
			Term{Kind: TRet},
			Term{Kind: TJmp, Target: 0},
			Term{Kind: TBrIf, True: 1, False: 0},
		),
		"diamond into a loop": prog(0,
			Term{Kind: TBrIf, True: 1, False: 2},
			Term{Kind: TJmp, Target: 3},
			Term{Kind: TJmp, Target: 3},
			Term{Kind: TBrIf, True: 0, False: 4},
			Term{Kind: TRet},
		),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got := LayoutOrder(p)
			if len(got) != len(p.Blocks) {
				t.Fatalf("order %v covers %d of %d blocks", got, len(got), len(p.Blocks))
			}
			seen := make([]bool, len(p.Blocks))
			for _, b := range got {
				if b < 0 || b >= len(p.Blocks) {
					t.Fatalf("order %v has out-of-range block %d", got, b)
				}
				if seen[b] {
					t.Fatalf("order %v repeats block %d", got, b)
				}
				seen[b] = true
			}
			if got[0] != p.Entry {
				t.Fatalf("order %v starts at %d, want the entry %d", got, got[0], p.Entry)
			}
		})
	}
}

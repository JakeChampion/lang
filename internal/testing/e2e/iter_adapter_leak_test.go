package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const (
	iterFilterSrc = `
import "core/iter" as iter;
function main(): i32 {
    let xs: i32[] = [5, 2, 8, 1, 4, 9].append(6);
    return iter.of(xs).filter((x: i32): boolean => { return x % 2 == 0; }).collect().len();
}
`
	iterMapSrc = `
import "core/iter" as iter;
function main(): i32 {
    let xs: i32[] = [5, 2, 8, 1, 4, 9].append(6);
    return iter.of(xs).map((x: i32): i32 => { return x + 1; }).collect().len();
}
`
	iterSumSrc = `
import "core/iter" as iter;
function main(): i32 {
    let xs: i32[] = [5, 2, 8, 1, 4, 9].append(6);
    return iter.sum(iter.of(xs));
}
`
	tupleCapturesParamSrc = `
function wrap(xs: u8[]): Option[(i32, u8[])] {
    return Some((1, xs));
}
function main(): i32 {
    let xs: u8[] = [1, 2];
    xs = xs.append(3 as u8);
    let n = 0;
    let i = 0;
    while (i < 3) {
        match (wrap(xs)) {
            Some(t) => { n = n + t.1.len(); },
            None => { n = n + 100; },
        }
        i = i + 1;
    }
    return n + xs.len();
}
`
	iterOfOnlySrc = `
import "core/iter" as iter;
function main(): i32 {
    let xs: i32[] = [5, 2, 8, 1, 4, 9].append(6);
    let it = iter.of(xs);
    return it.idx + xs.len();
}
`
)

func TestIterAdapterPairFormPayloadLeaksX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"filter", iterFilterSrc, 0},
		{"map", iterMapSrc, 0},
		{"sum", iterSumSrc, 0},
		{"tuple captures a param", tupleCapturesParamSrc, 0},
		{"iter.of alone", iterOfOnlySrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d — an iterator's next "+
					"returns a fresh payload, and the match arm that binds it releases "+
					"it; a leak here costs every combinator an allocation per element",
					got, tc.want)
			}
		})
	}
}

func TestPairFormPayloadCapturingParamStaysCorrectX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, tupleCapturesParamSrc, nil)
	_, _, got := runSplit(t, runX86_64Bin(runner, bin))
	if got != 12 {
		t.Errorf("exit %d, want 12 — the array a released payload captured was "+
			"freed or corrupted while main still held it", got)
	}
}

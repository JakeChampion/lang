package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `for` over an append-built array of structs with an rc field no longer
// costs the array its element walk in the AST lowering (FERN_SEM_IR=), so the
// element boxes and their `ops` buffers come back (#10161). The loop var is a
// borrow the loop ends before the exit sweep runs; the `refused` rows keep the
// walk off because the body would let an element or its buffer outlive it. They
// pin that the answer stays right under the sanitizer, and the AST leg's alloc
// and free counts: more frees there means the credit reached a shape the confinement
// proof does not cover.
const forArrStructDecls = `struct St { ops: i32[], n: i32 }
function build(): St[] {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    return hold;
}
`

var forArrStructCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
	refused  [2]int64 // allocs, frees on the AST leg
}{
	{"scalar_field", `function main(): i32 {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var s: i32 = 0;
    for h in hold { s = s + h.n; }
    return s;
}
`, 10, true, [2]int64{}},
	{"field_len", `function main(): i32 {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var s: i32 = 0;
    for h in hold { s = s + h.ops.len(); }
    return s;
}
`, 25, true, [2]int64{}},
	{"field_index", `function main(): i32 {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var s: i32 = 0;
    for h in hold { s = s + h.ops.len() * 10 + h.ops[0]; }
    return s % 100;
}
`, 60, true, [2]int64{}},
	{"refused_field_returned", `function last_ops(): i32[] {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var keep: i32[] = [];
    for h in hold { keep = h.ops; }
    return keep;
}
function main(): i32 {
    var k: i32[] = last_ops();
    var junk: i32[] = [9, 9, 9, 9, 9];
    return k[0] * 10 + k[4] + junk[0] - 9;
}
`, 44, false, [2]int64{15, 5}},
	{"refused_elem_escapes", `function main(): i32 {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var other: St[] = [];
    for h in hold { if (h.n > 2) { other = other.append(h); } }
    return other.len() * 10 + other[1].ops[0];
}
`, 24, false, [2]int64{15, 5}},
	{"refused_rebind_in_loop", `function main(): i32 {
    var hold: St[] = [];
    var j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    var s: i32 = 0;
    for h in hold { s = s + h.ops[0]; if (h.n == 1) { hold = hold.append(St { ops: [7], n: 7 }); } }
    return s * 10 + hold.len();
}
`, 106, false, [2]int64{16, 4}},
}

func TestSelfHostForArrStructReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range forArrStructCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(forArrStructDecls+tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, sem := range []string{"FERN_SEM_IR=", "FERN_SEM_IR=1"} {
				for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
					stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode, sem), nil)
					if exit != tc.want || forArrStructSanitizerFault(stderr, tc.balanced) {
						t.Fatalf("%s %s: exit = %d, want %d, and no sanitizer fault\n%s", sem, mode, exit, tc.want, stderr)
					}
					if mode == "FERN_LEAKCHECK=1" && tc.balanced {
						assertBalancedCensus(t, stderr)
					}
					if mode == "FERN_LEAKCHECK=1" && !tc.balanced && sem == "FERN_SEM_IR=" {
						assertRefusedCensus(t, stderr, tc.refused)
					}
				}
			}
		})
	}
}

func TestSelfHostForArrStructReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range forArrStructCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(forArrStructDecls+tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, sem := range []string{"FERN_SEM_IR=", "FERN_SEM_IR=1"} {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", sem))
				if exit != tc.want {
					t.Fatalf("%s: exit = %d, want %d\n%s", sem, exit, tc.want, stderr)
				}
				if tc.balanced {
					assertBalancedCensus(t, stderr)
				} else if sem == "FERN_SEM_IR=" {
					assertRefusedCensus(t, stderr, tc.refused)
				}
			}
		})
	}
}

// forArrStructSanitizerFault: a sanitizer report other than a leak, or any
// report at all when the row must balance.
func forArrStructSanitizerFault(stderr string, balanced bool) bool {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fern-sanitizer:") && (balanced || !strings.HasPrefix(line, "fern-sanitizer: leak ")) {
			return true
		}
	}
	return false
}

// assertRefusedCensus: a refused row keeps the shallow fallback, so its AST-leg
// census is exactly the shallow one.
func assertRefusedCensus(t *testing.T, stderr string, want [2]int64) {
	t.Helper()
	summary := leakSummaryLine(stderr)
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if got := [2]int64{allocs, frees}; got != want {
		t.Errorf("%s, want allocs=%d frees=%d — this row is REFUSED and must still take the shallow "+
			"fallback; more frees means the credit reaches a shape the confinement proof does not cover",
			summary, want[0], want[1])
	}
}

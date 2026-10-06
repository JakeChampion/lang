package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `for` over an append-built array of structs with an rc field keeps the
// array's element walk, so the element boxes and their `ops` buffers come back
// (#10161). The loop var is a borrow the loop ends before the exit sweep runs.
// A row that is not `balanced` lets an element or its buffer outlive the loop;
// it pins that the answer stays right under the sanitizer.
const forArrStructDecls = `struct St { ops: i32[], n: i32 }
function build(): St[] {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    return hold;
}
`

var forArrStructCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
}{
	{"scalar_field", `function main(): i32 {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let s: i32 = 0;
    for h in hold { s = s + h.n; }
    return s;
}
`, 10, true},
	{"field_len", `function main(): i32 {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let s: i32 = 0;
    for h in hold { s = s + h.ops.len(); }
    return s;
}
`, 25, true},
	{"field_index", `function main(): i32 {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let s: i32 = 0;
    for h in hold { s = s + h.ops.len() * 10 + h.ops[0]; }
    return s % 100;
}
`, 60, true},
	{"refused_field_returned", `function last_ops(): i32[] {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let keep: i32[] = [];
    for h in hold { keep = h.ops; }
    return keep;
}
function main(): i32 {
    let k: i32[] = last_ops();
    let junk: i32[] = [9, 9, 9, 9, 9];
    return k[0] * 10 + k[4] + junk[0] - 9;
}
`, 44, false},
	{"refused_elem_escapes", `function main(): i32 {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let other: St[] = [];
    for h in hold { if (h.n > 2) { other = other.append(h); } }
    return other.len() * 10 + other[1].ops[0];
}
`, 24, false},
	{"refused_rebind_in_loop", `function main(): i32 {
    let hold: St[] = [];
    let j: i32 = 0;
    while (j < 5) { hold = hold.append(St { ops: [j, 1, 2, 3, 4], n: j }); j = j + 1; }
    let s: i32 = 0;
    for h in hold { s = s + h.ops[0]; if (h.n == 1) { hold = hold.append(St { ops: [7], n: 7 }); } }
    return s * 10 + hold.len();
}
`, 106, false},
}

func TestSelfHostForArrStructReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range forArrStructCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(forArrStructDecls+tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, tc.balanced) {
					t.Fatalf("%s: exit = %d, want %d, and no sanitizer fault\n%s", mode, exit, tc.want, stderr)
				}
				if mode == "FERN_LEAKCHECK=1" && tc.balanced {
					assertBalancedCensus(t, stderr)
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
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			if tc.balanced {
				assertBalancedCensus(t, stderr)
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

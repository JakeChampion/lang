package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os/exec"
	"strings"
	"testing"
)

var ownedIfResultCases = []struct{ name, source, want string }{
	{"reader-borrowed-then", "struct Box { r: Reader, n: i32 }\nfunction wrap(r: Reader): Box { return Box { r: r, n: 1 }; }\nfunction take(own x: Box): i32 { return x.n; }\nfunction f(c: boolean, r: Reader): i32 { let x = if (c) { wrap(r) } else { wrap(stdin()) }; return take(x); }\nfunction main(): i32 { return 0; }\n", "E051"},
	{"reader-retained-valid", "struct Box { r: Reader, n: i32 }\nfunction wrap(r: Reader): Box { return Box { r: r, n: 1 }; }\nfunction take(own x: Box): i32 { return x.n; }\nfunction observe(r: Reader): i32 { return 0; }\nfunction f(c: boolean): i32 {\n let r = stdin();\n let x = if (c) { wrap(r) } else { wrap(stdin()) };\n let n = take(x);\n return n + observe(r);\n}\nfunction main(): i32 { return 0; }\n", "E051"},
	{"captured-direct-public-gap", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let get = (): Box => b; let x = if (c) { get() } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }\n", "E051"},
	{"checked-callback-arm", "struct H { fd: i32 }\nfunction (own h: H) close(): i32 { return h.fd; }\nstruct Box { h: H }\nfunction make(): H { return H { fd: 7 }; }\nfunction wrap(h: H): Box { return Box { h: h }; }\nfunction take(own b: Box): i32 { return b.h.fd; }\nfunction f(c: boolean): i32 { let get = (): own Box => Box { h: make() }; let x = if (c) { get() } else { wrap(make()) }; return take(x); } function main(): i32 { return 0; }", ""},
	{"owned-input-arms", "struct H { fd: i32 }\nfunction (own h: H) close(): i32 { return h.fd; }\nstruct Box { h: H }\nfunction make(): H { return H { fd: 7 }; }\nfunction wrap(h: H): Box { return Box { h: h }; }\nfunction take(own b: Box): i32 { return b.h.fd; }\nfunction move(own h: H): Box { return Box { h: h }; } function f(c: boolean, own h: H): i32 { let x = if (c) { move(h) } else { move(make()) }; return take(x); } function main(): i32 { return 0; }", ""},
	{"fresh-method-arms", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction (h: Handle) box(): Box { return wrap(h); } function f(c: boolean): i32 { let x = if (c) { make(3).box() } else { make(5).box() }; return take(x); } function main(): i32 { return f(true) + f(false) - 10; }", ""},
	{"borrowed-method-arm", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction (b: Box) same(): Box { return b; } function f(c: boolean, b: Box): i32 { let x = if (c) { b.same() } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"captured-callback-local-result", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let get = (): Box => b; let x = if (c) { let saved = get(); saved } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"shadowed-builtin-arm", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction stdin(b: Box): Box { return b; } function f(c: boolean, b: Box): i32 { let x = if (c) { stdin(b) } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"reported-reader", "struct Box { r: Reader, n: i32 }\nfunction wrap(r: Reader): Box { return Box { r: r, n: 1 }; }\nfunction take(own x: Box): i32 { return x.n; }\nfunction main(): i32 { let flag = args().len() > 5; let x = if (flag) { wrap(stdin()) } else { wrap(stdin()) }; return take(x) - 1; }", ""},
	{"direct-control", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction main(): i32 { let x = wrap(make(3)); return take(x) - 4; }", ""},
	{"fresh-call-arms", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { wrap(make(3)) } else { wrap(make(5)) }; return take(x); }\nfunction main(): i32 { return f(true) + f(false) - 10; }", ""},
	{"nested-call-arms", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { identity(wrap(make(3))) } else { identity(wrap(make(5))) }; return take(x); } function main(): i32 { return f(true) + f(false) - 10; }", ""},
	{"nested-if", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, d: boolean): i32 { let x = if (c) { if (d) { wrap(make(3)) } else { wrap(make(5)) } } else { wrap(make(7)) }; return take(x); } function main(): i32 { return f(true, true) + f(true, false) + f(false, false) - 18; }", ""},
	{"else-if", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, d: boolean): i32 { let x = if (c) { wrap(make(3)) } else if (d) { wrap(make(5)) } else { wrap(make(7)) }; return take(x); } function main(): i32 { return f(true, false) + f(false, true) + f(false, false) - 18; }", ""},
	{"local-call-arms", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { let n = 3; wrap(make(n)) } else { let n = 5; wrap(make(n)) }; return take(x); } function main(): i32 { return f(true) + f(false) - 10; }", ""},
	{"branch-local-result", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { let b = wrap(make(3)); b } else { let b = wrap(make(5)); b }; return take(x); } function main(): i32 { return f(true) + f(false) - 10; }", ""},
	{"borrowed-then", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let x = if (c) { identity(b) } else { wrap(make(5)) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"borrowed-else", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let x = if (c) { wrap(make(3)) } else { identity(b) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"borrowed-nested-call", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let x = if (c) { identity(identity(b)) } else { identity(wrap(make(5))) }; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"retained-source", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let b = wrap(make(3)); let x = if (c) { identity(b) } else { wrap(make(5)) }; return take(x) + b.n; } function main(): i32 { return 0; }", "E051"},
	{"retained-result", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { wrap(make(3)) } else { wrap(make(5)) }; let n = take(x); return n + x.n; } function main(): i32 { return 0; }", "E051"},
	{"borrowed-reassign", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean, b: Box): i32 { let x = if (c) { wrap(make(3)) } else { wrap(make(5)) }; x = b; return take(x); } function main(): i32 { return 0; }", "E051"},
	{"duplicate-move", "struct Handle { data: i32[] }\nfunction (own h: Handle) close(): i32 { return h.data[0]; }\nstruct Box { h: Handle, n: i32 }\nfunction make(n: i32): Handle { return Handle { data: [n] }; }\nfunction wrap(h: Handle): Box { return Box { h: h, n: 1 }; }\nfunction identity(b: Box): Box { return b; }\nfunction take(own b: Box): i32 { return b.h.data[0] + b.n; }\nfunction f(c: boolean): i32 { let x = if (c) { wrap(make(3)) } else { wrap(make(5)) }; return take(x) + take(x); } function main(): i32 { return 0; }", "E051"},
}

func testOwnedIfResults(t *testing.T, bin string, runner []string) {
	t.Helper()
	dir := t.TempDir()
	for _, tc := range ownedIfResultCases {
		t.Run(tc.name, func(t *testing.T) {
			var codes []string
			for _, d := range goCheckerDiags(t, dir, tc.source) {
				codes = append(codes, d.code)
			}
			if got := strings.Join(uniqueSortedCodes(codes), ","); got != tc.want {
				t.Fatalf("Go codes = %q, want %q", got, tc.want)
			}
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(tc.source)
			got := strings.Join(driverCodes(runCheckerDriver(t, command, tc.name)), ",")
			if got != tc.want {
				t.Fatalf("self-host codes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelfHostOwnedIfResultsX86_64(t *testing.T) {
	testOwnedIfResults(t, hostDriverBin(t, "drivers/checker_codes_run.fern"), nil)
}

func TestSelfHostOwnedIfResultsWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatal("wasmtime is required for ownership controls")
	}
	bin := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_codes_run.fern", e2eharness.TargetWasm32Wasi)
	testOwnedIfResults(t, bin, []string{wasmtime, "run"})
}

const ownedIfRuntimeSource = `struct Handle { data: i32[] }
@noinline function (own h: Handle) close(): i32 { return h.data[0]; }
struct Box { h: Handle, n: i32 }
@noinline function make(n: i32): Handle { return Handle { data: [n] }; }
@noinline function wrap(h: Handle): Box { return Box { h: h, n: 1 }; }
@noinline function identity(b: Box): Box { return b; }
@noinline function take(own b: Box): i32 { return b.h.data[0] + b.n; }

@noinline function choose(c: boolean): i32 {
 let b = if (c) { identity(wrap(make(3))) } else { let b = wrap(make(5)); b };
 return take(b);
}
function main(): i32 {
 let total = 0;
 for i in 0..12 { total += choose(i % 2 == 0); }
 return total - 60;
}
`

func TestSelfHostOwnedIfResultsRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, ownedIfRuntimeSource, target, "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

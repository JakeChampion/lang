package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An unannotated `var x = E?` binding had no type in the self-host checker —
// `?` fell through check_expr's unary arm as unknown — so the semantic lowering
// refused every function holding one and the AST lowering stood, which admits
// a `?`-bound payload as an owner only for a `: string`-annotated binding over
// a user producer. Every other spelling leaked its payload per iteration:
// `r.read_chunk(n)?` and `r.read_line()?` (#8803), a user Option producer, and
// a `@try` enum, generic or not.
//
// Each program runs its shape in a loop and exits with a value derived from
// every payload, so the census must balance and the answer must be native's.
var tryBindingReclaimCases = []struct {
	name, src string
	stdin     []byte
	want      int
}{
	{"read_chunk", `function drain(r: Reader): Result[i32, IoError] {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 24) {
        var chunk = r.read_chunk(1024)?;
        acc = acc + chunk.len();
        i = i + 1;
    }
    return Ok(acc);
}
function main(): i32 {
    match (drain(stdin())) { Ok(n) => { return n % 101; }, Err(e) => { return 9; } }
}
`, bytes.Repeat([]byte{'x'}, 24*1024), 24 * 1024 % 101},
	{"read_line", `function lines(r: Reader): Option[i32] {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var l = r.read_line()?;
        acc = acc + l.len();
        i = i + 1;
    }
    return Some(acc);
}
function main(): i32 {
    match (lines(stdin())) { Some(n) => { return n; }, None => { return 9; } }
}
`, []byte("aaaa\nbbbbbbbb\ncc\n"), 17},
	{"user_option", `import "std/i32";
function mk(i: i32): Option[string] { if (i < 0) { return None; } return Some("v" + i.to_string()); }
function pick(i: i32): Option[i32] { var s = mk(i)?; return Some(s.len()); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { match (pick(i)) { Some(k) => { n = n + k; }, None => { return 9; } } i = i + 1; }
    return n % 101;
}
`, nil, 140 % 101},
	{"try_enum", `import "std/i32";
@try
enum Got { Have(string), Miss }
@try
enum MyOpt[T] { Here(T), Gone }
function g(i: i32): Got { if (i < 0) { return Miss; } return Have("n" + i.to_string()); }
function h(i: i32): MyOpt[string] { if (i < 0) { return Gone; } return Here("m" + i.to_string()); }
function useg(i: i32): Got { var s = g(i)?; return Have(s + "!"); }
function useh(i: i32): MyOpt[string] { var s = h(i)?; return Here(s + "?"); }
function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        match (useg(i)) { Have(s) => { n = n + s.len(); }, Miss => { return 1; } }
        match (useh(i)) { Here(s) => { n = n + s.len(); }, Gone => { return 2; } }
        i = i + 1;
    }
    return n % 101;
}
`, nil, 380 % 101},
}

func writeTryBindingCase(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostTryBindingReclaimX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tryBindingReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			bin := cli.x86Binary(t, writeTryBindingCase(t, tc.name, tc.src), "FERN_LEAKCHECK=1")
			stderr, exit := runWithStdin(t, cli.runner, bin, tc.stdin)
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostTryBindingReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm try-binding reclaim")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range tryBindingReclaimCases {
		if tc.stdin != nil {
			continue // runWasmCensus gives the module no stdin
		}
		t.Run(tc.name, func(t *testing.T) {
			wat := cli.emit(t, writeTryBindingCase(t, tc.name, tc.src), "wasm32-wasi", "FERN_LEAKCHECK=1")
			stderr, exit := runWasmCensus(t, wat)
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

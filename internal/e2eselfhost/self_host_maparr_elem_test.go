package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapArrElemCases pin the map ELEMENT of a `Map[K, V][]` reaching every
// receiver position, and the array itself reaching none of them.
//
// #7195 fixed the two IDENT readers that typed a map-array as one map. The
// element side was the other half: a `for` loop variable carried no map type at
// all, and the map-op dispatch's ExprIndex arm only understood an ident base, so
// `t.0[i].get(k)` and `r.rows[i].get(k)` bailed with `unknown symbol i32.get_or`.
//
// Widening that arm then exposed the #7195 trap on two more readers: the
// struct-field and tuple-element arms test the receiver with is_map_type_name,
// which is a bare `Map[` prefix match, so a `Map[K, V][]` FIELD passed it and
// `r.rows.len()` lowered to op_map_len over array memory. That segfault was
// always there — the earlier bail on the element read just stopped anyone
// reaching it. Both arms now require a non-array spelling, matching the ident
// arms' slot_map_type.
var mapArrElemCases = []struct {
	name string
	src  string
	want int
}{
	// Loop variable. Bailed before: the foreach binds the element directly and
	// marked struct / opt / tuple / arrarr element kinds but never a map.
	{"maparr-foreach", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var ms: Map[string, i32][] = [m, m];
    var acc: i32 = 0;
    for x in ms { acc = acc + x.get_or("k", 0); }
    return acc + ms.len();
}`, 16},
	// Tuple-element base. Bailed before.
	{"maparr-tuple-elem-index", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var ms: Map[string, i32][] = [m];
    var t: (Map[string, i32][], i32) = (ms, 3);
    return t.0[0].get_or("k", 0) + t.1;
}`, 10},
	// Struct-field base, reading the element AND the array's own length. Bailed
	// before on the element read; once that worked the `.len()` segfaulted.
	{"maparr-struct-field-index-and-len", `struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var r: Reg = Reg { rows: [m] };
    return r.rows[0].get_or("k", 0) + r.rows.len();
}`, 8},
	// The `.len()` alone, which is the segfault with nothing else in the way.
	{"maparr-struct-field-len-only", `struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var r: Reg = Reg { rows: [m, m, m] };
    return r.rows.len();
}`, 3},
	// NON-VACUITY on the two new `!is_array_type_name` guards: a genuine Map
	// STRUCT FIELD must still dispatch every map op off the field.
	{"plain-map-struct-field-unchanged", `struct Cfg { caps: Map[string, i32] }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    var c: Cfg = Cfg { caps: m };
    return c.caps.len() + c.caps.get_or("a", 0) + c.caps.get_or("b", 0);
}`, 9},
	// The same for a genuine Map TUPLE ELEMENT.
	{"plain-map-tuple-elem-unchanged", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 5);
    var t: (Map[string, i32], i32) = (m, 2);
    return t.0.len() + t.0.get_or("a", 0) + t.1;
}`, 8},
	// Churn over the loop-var path, so a mis-typed dispatch cannot hide behind
	// a single-shot value check.
	{"maparr-foreach-churn", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var ms: Map[string, i32][] = [m, m];
        for x in ms { acc = (acc + x.get_or("k", 0)) % 83; }
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 83;
}`, 12},
	// A DIRECT CALL receiver whose return type is `Map[K, V][]`. map_ret_fns_of
	// registered any `Map[`-prefixed return type as map-returning, so the
	// un-bound `mk().len()` receiver typed as a map — SEGFAULT before. Binding
	// it to a local first (below) was already safe, because the local's slot is
	// array-marked and slot_map_type declines it.
	{"maparr-call-receiver-len", `function mk(): Map[string, i32][] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return [m, m];
}
function main(): i32 {
    return mk().len();
}`, 2},
	{"maparr-call-bound-then-used", `function mk(): Map[string, i32][] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return [m, m];
}
function main(): i32 {
    var a: Map[string, i32][] = mk();
    return a.len() + a[0].get_or("k", 0);
}`, 9},
	// A map-ARRAY PARAM. Already correct, and now pinned: the param column
	// records the ELEMENT map type like every other map-array site rather than
	// the array spelling it previously stored and happened to survive.
	{"maparr-param-len", `function count(ms: Map[string, i32][]): i32 {
    return ms.len();
}
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return count([m, m, m]);
}`, 3},
	{"maparr-param-index-and-len", `function pick(ms: Map[string, i32][]): i32 {
    return ms[0].get_or("k", 0) + ms.len();
}
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return pick([m, m]);
}`, 9},
	// A map-array reaching a tuple through a struct field, then read as an array.
	{"maparr-struct-field-into-tuple", `struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var r: Reg = Reg { rows: [m] };
    var t: (Map[string, i32][], i32) = (r.rows, 5);
    return t.0.len() + t.1;
}`, 6},
}

const mapArrElemFailFmt = "%s = %d, want %d (-1 = died on a signal: a map op dispatched on an array box; 99 = over-release; 97 = value corrupted)"

func runMapArrElemStrictIR(t *testing.T, driverBin string, runner []string, src string, extra ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, extra...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), extra...)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src + "\n"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_STRICT_IR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1")
	_ = cmd.Run()
	if stdout.Len() == 0 {
		t.Fatalf("did not lower under FERN_STRICT_IR=1 (exit %d):\n%s", cmd.ProcessState.ExitCode(), stderr.String())
	}
	return stdout.Bytes()
}

func TestSelfHostMapArrElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapArrElemStrictIR(t, driverBin, runner, tc.src)
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapArrElemStrictIR(t, driverBin, x86runner, tc.src, "-target", "arm64-linux")
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map-array element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runMapArrElemStrictIR(t, driverBin, runner, tc.src, "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", watFile)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

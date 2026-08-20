package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapArrIdentCases pin a `Map[K, V][]` IDENT not being typed as a single map.
//
// A map-array slot records its ELEMENT map type in the same column a plain map
// local uses, so that `ms[i].get(k)` can resolve K/V. Two readers took that
// column for the name itself without asking whether the slot is an array, so
// `ms` typed as one `Map[K, V]` and `ms.len()` lowered to op_map_len over array
// memory — a SEGFAULT, not a bail, where native answers 1.
//
// Both readers now go through LowerState.slot_map_type, which answers "" for an
// array slot. The array-ELEMENT readers are unaffected: they pair map_type_of
// with is_arr_slot explicitly, which is how `ms[i].get(k)` already worked and
// why it is a control here rather than a fix.
//
// Not covered, and still an honest bail rather than a miscompile: `for x in ms`
// and a map-array in a tuple element or struct field. Those need a map ELEMENT
// kind the array side does not carry — a separate slice.
var mapArrIdentCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. SEGFAULT before.
	{"maparr-len", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var ms: Map[string, i32][] = [m];
    return ms.len();
}`, 1},
	// The gate plus the element read, so the two paths are exercised on one
	// slot: `ms` as an array, `ms[0]` as a map. SEGFAULT before.
	{"maparr-len-and-index", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var ms: Map[string, i32][] = [m];
    return ms.len() + ms[0].get_or("k", 0);
}`, 8},
	// CONTROL: the element read alone already resolved through the ExprIndex
	// arm's own is_arr_slot check. It must keep working — a fix that answered
	// "" everywhere for the column would break this.
	{"maparr-index-only", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    var ms: Map[string, i32][] = [m];
    return ms[0].get_or("k", 0);
}`, 7},
	// CONTROL: a genuine map local must still dispatch every map op off its
	// ident. This is the shape slot_map_type exists to keep answering.
	{"plain-map-ops-unchanged", `function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    var got: i32 = 0;
    match (m.get("b")) { Some(v) => { got = v; }, None => {} }
    return m.len() + m.get_or("a", 0) + got;
}`, 9},
	// A churn loop over both paths: the reads must stay balanced, so a
	// mis-typed dispatch cannot hide behind a single-shot value check.
	{"maparr-churn-balanced", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var ms: Map[string, i32][] = [m];
        acc = (acc + ms.len() + ms[0].get_or("k", 0)) % 83;
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
}`, 10},
}

const mapArrIdentFailFmt = "%s = %d, want %d (-1 = died on a signal, which on the parent is the segfault from a map op dispatched on an array box; 99 = over-release; 97 = value corrupted)"

func runMapArrIdentStrictIR(t *testing.T, driverBin string, runner []string, src string, extra ...string) []byte {
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

func TestSelfHostMapArrIdentIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapArrIdentStrictIR(t, driverBin, runner, tc.src)
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrIdentIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapArrIdentStrictIR(t, driverBin, x86runner, tc.src, "-target", "arm64-linux")
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrIdentWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map-array ident wasm IR e2e")
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

	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runMapArrIdentStrictIR(t, driverBin, runner, tc.src, "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", watFile)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

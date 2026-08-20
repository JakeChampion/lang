package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapPayloadMatchCases pin `match` on an Option/Result whose payload is a Map.
//
// The built-in-variant arm of lower_stmt_match lists every payload shape it
// accepts, and Map was absent — so the whole function refused to lower:
//
//	FERN_STRICT_IR: churn (did not lower: `match`)
//
// while native compiled and ran the same program. It was an asymmetry rather
// than a missing capability: the USER-ENUM variant arm in the same function
// already binds a Map payload via mark_map_type (`JObject(Map[string, JsonValue])`).
// A Map box is a heap pointer at offset 8, exactly like the struct / tuple /
// array payloads that arm already reads through op_opt_payload.
//
// Narrowed before the fix, on x86-64: the bail fired with the payload bound and
// used AND with it bound and unused; `Option[i32[]]` lowered; an Option[Map]
// that was never matched lowered; a Map passed to a function lowered. So the
// payload-type gate was the only thing refusing.
//
// The binding is BORROWED from the Option/Result box. mark_map_type sets no
// is_arr, so it stays out of the exit dec-sweep on its own — the array branches
// need mark_borrowed_arr (#6049) only because mark_arr would have swept them.
var mapPayloadMatchCases = []struct {
	name string
	src  string
	want int
	// bails: the driver must still REFUSE this one. Only the array-of-map
	// payload sets it — see opt-map-array-payload-refused.
	bails bool
}{
	// The gate. Refused to lower at all before; native returns 90.
	{"opt-map-payload-match", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var o: Option[Map[string, i32]] = Some(m);
        var r: i32 = 0;
        match (o) { Some(v) => { r = v.get_or("k", 0) + v.len(); }, None => {} }
        acc = (acc + r) % 83;
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
}`, 10, false},
	// The payload BOUND BUT UNUSED still went through the gate, because `hb` is
	// true for any named binding. It must lower now too.
	{"opt-map-payload-unused", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var o: Option[Map[string, i32]] = Some(m);
        var r: i32 = 0;
        match (o) { Some(v) => { r = 1; }, None => {} }
        acc = (acc + r + m.get_or("k", 0)) % 83;
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
}`, 10, false},
	// The Ok side of a Result. opt_payload_type returns T here.
	{"result-map-ok-payload", `function pick(n: i32): Result[Map[string, i32], string] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", n);
    if (n < 0) { return Err("neg"); }
    return Ok(m);
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var r: i32 = 0;
        match (pick(i)) { Ok(v) => { r = v.get_or("k", 0); }, Err(e) => { r = e.len(); } }
        acc = (acc + r) % 83;
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
}`, 6, false},
	// The Err side. opt_payload_type splits on the first TOP-LEVEL comma, so the
	// commas inside `Map[string, i32]` do not confuse the T/E split — one
	// predicate covers both arms.
	{"result-map-err-payload", `function pick(n: i32): Result[i32, Map[string, i32]] {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("k", n);
    if (n % 2 == 0) { return Err(m); }
    return Ok(n);
}
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var r: i32 = 0;
        match (pick(i)) { Ok(v) => { r = v; }, Err(e) => { r = e.get_or("k", 0) + e.len(); } }
        acc = (acc + r) % 83;
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
}`, 8, false},
	// A Map whose VALUE type is an array — the payload spelling carries nested
	// brackets, and the bound slot must still dispatch `.get(k)` as a map op.
	{"opt-map-of-array-payload", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32[]] = map_new(4);
        m = m.insert("k", [i, i + 2]);
        var o: Option[Map[string, i32[]]] = Some(m);
        var r: i32 = 0;
        match (o) {
            Some(v) => { match (v.get("k")) { Some(xs) => { r = xs[0] + xs[1]; }, None => {} } },
            None => {}
        }
        acc = (acc + r) % 83;
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
}`, 20, false},
	// `Map[K, V][]` is an ARRAY OF MAPS, and it must stay refused.
	//
	// This case is why some_opt_type declines an array payload it cannot spell.
	// A map-array slot records the ELEMENT map type in the map column (so
	// `ms[i].get(k)` resolves), so elem_type_tag answers the bare `Map[K, V]`
	// for `ms` itself — and that INFERENCE beats the binding's own correct
	// `Option[Map[K, V][]]` annotation. The payload gate then sees a plain map
	// and admits an array box as one: `v.len()` compiled to a map length over
	// array memory and SEGFAULTED (native answers 4). With the annotation
	// winning, the gate sees the array spelling and refuses, which is what it
	// did before Map payloads were admitted at all.
	//
	// Asserted on the refusal REASON, not just a non-zero exit: the miscompile
	// above lowered cleanly, so only "did it lower, and why not" separates the
	// two outcomes.
	{"opt-map-array-payload-refused", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        var ms: Map[string, i32][] = [m];
        var o: Option[Map[string, i32][]] = Some(ms);
        var r: i32 = 0;
        match (o) { Some(v) => { r = v.len(); }, None => {} }
        acc = (acc + r) % 83;
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
}`, 0, true},
}

const mapPayloadMatchFailFmt = "%s = %d, want %d (99 = over-release; 97 = value corrupted)"

// runMapPayloadStrictIR emits a case under FERN_STRICT_IR=1 and asserts on
// WHETHER IT LOWERED, which is the half of this suite's contract an exit code
// cannot carry: the bug was a refusal to lower, so a regression would otherwise
// come back as a test that never runs its program rather than one that fails.
// It returns nil for a case declared `bails` (nothing left to execute).
func runMapPayloadStrictIR(t *testing.T, bails bool, driverBin string, runner []string, src string, extra ...string) []byte {
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
	if bails {
		if stdout.Len() != 0 {
			t.Fatal("lowered, but the array-of-map payload must still be refused: " +
				"admitting it would mark an array slot as a map")
		}
		if !strings.Contains(stderr.String(), "did not lower: `match`") {
			t.Fatalf("refused for the wrong reason (exit %d):\n%s", cmd.ProcessState.ExitCode(), stderr.String())
		}
		return nil
	}
	if stdout.Len() == 0 {
		t.Fatalf("did not lower under FERN_STRICT_IR=1 (exit %d):\n%s", cmd.ProcessState.ExitCode(), stderr.String())
	}
	return stdout.Bytes()
}

func TestSelfHostMapPayloadMatchIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapPayloadStrictIR(t, tc.bails, driverBin, runner, tc.src)
			if asm == nil {
				return
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapPayloadMatchIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runMapPayloadStrictIR(t, tc.bails, driverBin, x86runner, tc.src, "-target", "arm64-linux")
			if asm == nil {
				return
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapPayloadMatchWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping map payload match wasm IR e2e")
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

	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runMapPayloadStrictIR(t, tc.bails, driverBin, runner, tc.src, "-ir")
			if wat == nil {
				return
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", watFile)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

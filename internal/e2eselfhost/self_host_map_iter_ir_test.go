package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapIterIRX86_64 verifies single-var map iteration
// `for k in m.keys()` / `for v in m.values()` lowers through the IR path. The
// keys()/values() array is a BORROW of the map's internal storage, so the
// snapshot local is deliberately NOT array-marked (the exit-sweep must not free
// it) and arr_len/arr_get are emitted explicitly — mirroring the 2-var
// `for k, v in m` form. Without this, the iterable is an array-returning call so
// the generic snapshot path would (wrongly) treat it as an owned array; the
// dedicated handler runs first.
//
// Cases cover string and i32 keys, i32 values, and a `continue` in the body.
// Size checks prove the IR path was taken (a bail pulls in the ~43 KB AST map
// runtime); exit codes pin correctness incl. the borrow not being double-freed.
func TestSelfHostMapIterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		{"keys-string", `function f(): i32 { var m: Map[string, i32] = map_new(0); m = m.insert("a", 7); m = m.insert("b", 8); var s: i32 = 0; for k in m.keys() { s = s + m.get_or(k, 0); } return s; }
function main(): i32 { return f(); }`, 15},
		{"values-i32", `function f(): i32 { var m: Map[string, i32] = map_new(0); m = m.insert("a", 7); m = m.insert("b", 8); var s: i32 = 0; for v in m.values() { s = s + v; } return s; }
function main(): i32 { return f(); }`, 15},
		{"keys-i32", `function f(): i32 { var m: Map[i32, i32] = map_new_i32(0); m = m.insert(3, 10); m = m.insert(4, 20); var s: i32 = 0; for k in m.keys() { s = s + k; } return s; }
function main(): i32 { return f(); }`, 7},
		{"keys-continue", `function f(): i32 { var m: Map[i32, i32] = map_new_i32(0); m = m.insert(1, 0); m = m.insert(2, 0); m = m.insert(3, 0); var s: i32 = 0; for k in m.keys() { if (k % 2 == 0) { continue; } s = s + k; } return s; }
function main(): i32 { return f(); }`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			// 30 KB keeps a wide margin below the ~40 KB+ AST-bail signature
			// while tolerating IR-runtime growth: the 20 KB threshold tripped
			// at ~20.2–21 KB when #4355's always-emitted
			// __fn___fern_str_arr_free helper landed (bumped to 25 KB), and the
			// 25 KB threshold in turn tripped at ~25.1–25.4 KB (keys-string /
			// keys-continue) as later IR-runtime growth (the fip/fbip + RC
			// reclamation passes) landed — the programs still fully on the IR
			// path (values-i32 / keys-i32 stay well under and run correctly, and
			// 25 KB is nowhere near the AST-bail size).
			if len(asm) == 0 || len(asm) > 30000 {
				t.Fatalf("asm is %d bytes — expected IR output; the map-iteration module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "map_iter_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d", code, tc.want)
			}
		})
	}
}

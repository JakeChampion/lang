package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateScalarCases extend the typed-IR annotation (#5531) from expr_is_f64 to
// the unsigned-integer "is the result type X?" predicates that key off a call: a
// u32-returning call (expr_is_u32) and a u64-returning call (expr_is_u64). The
// annotate pass already stamps every scalar tag onto ExprCall.ty; this pins each
// predicate's ExprCall arm reading c.ty instead of its is_*_ret_fn registry. The
// shift is applied to the CALL RESULT directly (not a typed local) so the
// ExprCall arm is the code path under test, and unsigned-ness makes the shift
// result observable (a signed shift on a top-bit-set value differs).
var annotateScalarCases = []struct {
	name string
	src  string
}{
	// u32-returning call shifted directly: an UNSIGNED >> (bit 31 set) — a signed
	// shift would sign-extend to a different value.
	{"u32_call_shift", `function big(): u32 { return 4000000000 as u32; }
function main(): i32 { return (big() >> 1) as i32; }`}, // 2000000000 as i32 = 2000000000
	// u64-returning call shifted directly: an UNSIGNED >> on a bit-63-set value.
	{"u64_call_shift", `function bigu(): u64 { return 18000000000000000000 as u64; }
function main(): i32 { return (bigu() >> 40) as i32; }`}, // 216
}

// TestSelfHostAnnotateScalarIR_X86_64 pins the checker-stamped result type feeding
// irlower's expr_is_u32 / expr_is_u64 through the IR path (#5531).
func TestSelfHostAnnotateScalarIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annscal_"+tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

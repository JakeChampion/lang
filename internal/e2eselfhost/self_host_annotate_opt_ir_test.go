package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateOptCases extend the typed-IR annotation (#5531) to Option/Result-valued
// calls. This required a checker change: TypeUnion gained an `args` field so a
// builtin generic union stops dropping its type argument (Option[i32] →
// TypeUnion{"Option", [i32]}); type_to_irtag then reconstructs the "Option[T]" /
// "Result[T, E]" tag, and irlower's try_opt_type reads it off c.ty instead of
// re-deriving via opt_ret_type + argref/closure resolution. try_opt_type drives
// `match` scrutinee routing and the `?` operator's payload/error typing.
//
// Option/Result box their payload on the heap; the binary runs via the
// X86_64Tooling runner prefix (nil on an x86_64 host).
var annotateOptCases = []struct {
	name string
	src  string
}{
	// match on an Option-returning call.
	{"match_option", `function pick(n: i32): Option[i32] { if (n > 0) { return Some(n * 10); } return None; }
function main(): i32 { match (pick(4)) { Some(v) => { return v; }, None => { return 0; } } }`}, // 40
	// `?` propagation through Result-returning calls.
	{"try_result", `function half(n: i32): Result[i32, i32] { if (n % 2 == 0) { return Ok(n / 2); } return Err(1); }
function run(): Result[i32, i32] { var x: i32 = half(8)?; var y: i32 = half(4)?; return Ok(x + y); }
function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return 99; } } }`}, // 4 + 2 = 6
	// Option[string] payload used as a string (v.len()).
	{"match_option_string", `function name_of(n: i32): Option[string] { if (n == 1) { return Some("hello"); } return None; }
function main(): i32 { match (name_of(1)) { Some(v) => { return v.len(); }, None => { return 0; } } }`}, // 5
}

// TestSelfHostAnnotateOptIR_X86_64 pins the checker-stamped Option/Result result
// type feeding irlower's try_opt_type through the IR path (#5531).
func TestSelfHostAnnotateOptIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range annotateOptCases {
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
			progBin := buildBin(t, gcc, dir, "annopt_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateStrCases extend the typed-IR annotation (#5531) to string-valued
// calls. The annotate pass stamps ExprCall.ty = "string" for a string-returning
// call; expr_is_str's ExprCall arm reads it instead of re-deriving via the
// str_ret_fns registry. Each program routes a string-returning call's result
// into a string-typed use (.len() / concat), oracle-checked against the interp.
//
// String programs allocate a heap (arena mmap): under binfmt-direct exec in a
// cross-arch container that SIGSEGVs, but it runs correctly under explicit qemu
// and native x86_64 — so the binary runs via the X86_64Tooling runner prefix
// (nil on an x86_64 host).
var annotateStrCases = []struct {
	name string
	src  string
}{
	// string-returning call bound to a local, then .len().
	{"call_len", `function greet(n: string): string { return "hi " + n; }
function main(): i32 { var g: string = greet("bob"); return g.len(); }`}, // 6
	// concat of two string-returning call results (each must type as a string).
	{"call_concat", `function pfx(n: string): string { return "x" + n; }
function main(): i32 { var g: string = pfx("ab") + pfx("cde"); return g.len(); }`}, // 3 + 4 = 7
	// string call result used directly in a concat then measured.
	{"call_direct_concat", `function tag(n: string): string { return "[" + n + "]"; }
function main(): i32 { var g: string = tag("hi") + "!"; return g.len(); }`}, // "[hi]!" = 5
}

// TestSelfHostAnnotateStrIR_X86_64 pins the checker-stamped string result type
// feeding irlower's expr_is_str through the IR path (#5531).
func TestSelfHostAnnotateStrIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range annotateStrCases {
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
			progBin := buildBin(t, gcc, dir, "annstr_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

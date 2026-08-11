package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSelfHostPlatformsCapabilityRules exercises the self-host's target
// capability boundary (examples/self_host/platforms.fern, #6633) — the port of
// native's internal/platforms.
//
// The driver asserts each rule in BOTH directions: a program the target may
// not compile, and one it may. The permissive half carries the weight. A
// capability gate that fires on a valid program refuses code every backend
// would have built, and unlike a missing gate it cannot be worked around.
//
// Exit 0 means every assertion held. A non-zero code identifies the case, so a
// regression names itself without a stdout diff.
func TestSelfHostPlatformsCapabilityRules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("platforms_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "platforms_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "platforms_run.fern", "platforms_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("platforms_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("platforms_run exit code = %d, want 0 — that code is the failing assertion's id in platforms_run.fern", code)
	}
	if want := "platforms: every capability rule agrees"; !strings.Contains(string(out), want) {
		t.Errorf("platforms_run stdout = %q, want it to contain %q", out, want)
	}
}

// e066Sites reduces a compiler's output to the E066 diagnostics it emitted, as
// `line:col` (or a bare marker when the diagnostic carries no position, which
// is how both compilers report a violation inside an imported module).
//
// The MESSAGE is deliberately not compared. It names the target, and the two
// compilers spell targets differently until #6635 lands (`wasm` vs
// `wasm32-wasi`), so the code and the position are what parity means here —
// the same rule firing at the same place.
func e066Sites(out string) []string {
	var sites []string
	re := regexp.MustCompile(`(?:^|[: ])(\d+):(\d+): error\[E066\]`)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "error[E066]") {
			continue
		}
		if m := re.FindStringSubmatch(line); m != nil {
			sites = append(sites, m[1]+":"+m[2])
			continue
		}
		sites = append(sites, "<no position>")
	}
	sort.Strings(sites)
	return sites
}

// TestSelfHostTargetCapabilityDifferentialX86_64 is the parity gate: for a
// program reaching for a host capability, native and the self-host must agree
// on whether the target provides it.
//
// This is the divergence shape that actually blocks "the self-host is the only
// compiler" — not a message the self-host words differently, but a source file
// that builds under one compiler and not the other. Before #6633 the self-host
// had no capability layer at all, so every case below built clean there and
// was refused by native.
func TestSelfHostTargetCapabilityDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range []struct {
		name string
		// selfHostTarget / nativeTarget name the same machine; the spellings
		// differ until #6635 renames the self-host's.
		selfHostTarget string
		nativeTarget   string
		src            string
	}{
		// The wasi CLI world has no process model, so fork/waitpid/exec are
		// refused there and granted on the hosted natives.
		{"proc-fork-wasm", "wasm", "wasm32-wasi", "function main(): i32 {\n    var pid: i32 = proc_fork();\n    return pid;\n}\n"},
		{"proc-fork-native-ok", "x86-64", "x86-64-linux", "function main(): i32 {\n    var pid: i32 = proc_fork();\n    return pid;\n}\n"},
		// The bump-arena checkpoint rewinds a heap pointer only the natives
		// keep. Reading the cursor (`__heap_bump_bytes`) is ungated, which is
		// what keeps this from being "anything heap-shaped is native-only".
		{"heap-mark-wasm", "wasm", "wasm32-wasi", "function main(): i32 {\n    var m: i64 = __heap_mark();\n    __heap_release_to(m);\n    return 0;\n}\n"},
		{"heap-bump-bytes-wasm-ok", "wasm", "wasm32-wasi", "function main(): i32 {\n    return __heap_bump_bytes();\n}\n"},
		// No compiled target provides `subprocess` — it is interp-only, so
		// this is refused on the natives too.
		{"subprocess-native", "x86-64", "x86-64-linux", "function main(): i32 {\n    var argv: string[] = [];\n    var r: i32 = run_it(argv);\n    return r;\n}\nfunction run_it(argv: string[]): i32 {\n    subprocess(\"ls\", argv, \"\");\n    return 0;\n}\n"},
		// The capabilities a wasm program legitimately has: a filesystem, a
		// clock, entropy, stdout. If the gate fired on these it would refuse
		// most real programs.
		{"fs-wasm-ok", "wasm", "wasm32-wasi", "function main(): i32 {\n    match (read_file(\"x\")) {\n        Ok(s) => { return s.len(); },\n        Err(e) => { return 1; },\n    }\n    return 0;\n}\n"},
		{"print-wasm-ok", "wasm", "wasm32-wasi", "function main(): i32 {\n    print(\"hi\");\n    return 0;\n}\n"},
		{"clock-wasm-ok", "wasm", "wasm32-wasi", "function main(): i32 {\n    return (now_unix_ms() as i32);\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeOut, _ := exec.Command(nativeBin, "-check", "-target", c.nativeTarget, src).CombinedOutput()
			shOut, _ := exec.Command(driverBin, "-check", "-target", c.selfHostTarget, src, stdlib).CombinedOutput()

			want := e066Sites(string(nativeOut))
			got := e066Sites(string(shOut))
			if strings.Join(want, " ") != strings.Join(got, " ") {
				t.Errorf("E066 sites differ: native %v, self-host %v\n--- native ---\n%s\n--- self-host ---\n%s",
					want, got, nativeOut, shOut)
			}
		})
	}
}

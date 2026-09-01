package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEmbedBundleAndSubstitution exercises the self-host's `-embed`
// asset bundle (examples/self_host/embed.fern, #6643) — the port of native's
// internal/embed plus the asset half of internal/constfold.
//
// The driver builds a real directory under temp_dir and walks it, because
// sorted order, nested names and the empty-vs-absent bundle are properties of
// the walk rather than of a hand-built value.
//
// Exit 0 means every assertion held. A non-zero code identifies the case, so a
// regression names itself without a stdout diff.
func TestSelfHostEmbedBundleAndSubstitution(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("embed_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "embed_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "embed_run.fern", "embed_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("embed_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("embed_run exit code = %d, want 0 — that code is the failing assertion's id in embed_run.fern\n%s", code, out)
	}
	if want := "embed: bundle and substitution agree"; !strings.Contains(string(out), want) {
		t.Errorf("embed_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostEmbedMatchesNative is the parity gate: the same source, the same
// embed directory, and the same answer out of both compilers.
//
// Compared on the COMPILED PROGRAM's exit code rather than on the compilers'
// output, because that is what the feature promises — the bytes of a file
// reaching the running program — and because the two word a diagnostic
// differently. What the accept/reject rows assert is only that they agree on
// whether the program is a program at all; matching an exact message across
// two compilers is a diagnostic-format comparison, not an asset one.
//
// The rejections carry the weight here. A missing asset that compiled into an
// empty string would be a program that builds, runs, and serves nothing —
// which is exactly what an embedded stdlib must never do (#6643).
func TestSelfHostEmbedMatchesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("embed differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	// The bundle both compilers read. Nested, unsorted on disk, and with a
	// name that is a strict prefix of nothing so `suggest` has a real choice.
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	for name, body := range map[string]string{
		"z.txt":     "ZZZ",
		"a.txt":     "AAA",
		"sub/b.txt": "BB",
	} {
		if err := os.WriteFile(filepath.Join(assets, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
	}
	// An empty directory is a legitimate bundle, and not the same thing as
	// having passed no -embed at all.
	empty := filepath.Join(dir, "empty-assets")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}

	for _, c := range []struct {
		name string
		// embedDir is passed to -embed; empty means the flag is omitted.
		embedDir string
		src      string
		// wantExit is the compiled program's exit code, and -1 means the
		// program must not compile at all.
		wantExit int
	}{
		// 3 + 2 = 5: the bytes of two files, one of them nested.
		{"asset-one", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\").len() + __fern_asset(\"sub/b.txt\").len();\n}\n", 5},
		// Names sorted, contents alongside: 5+9+5 name bytes, 3+2+3 contents.
		{"assets-all", assets, "function main(): i32 {\n    var n: i32 = 0;\n    for a in __fern_assets() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 27},
		// Sorted order is observable, and it is what keeps the emitted program
		// identical across hosts — a filesystem-order walk would not be.
		{"assets-sorted", assets, "function main(): i32 {\n    var first: string = \"\";\n    for a in __fern_assets() { if (first.len() == 0) { first = a.0; } }\n    if (first == \"a.txt\") { return 7; }\n    return 9;\n}\n", 7},
		// An empty bundle yields an empty typed array, so the loop body simply
		// never runs. The element type has to be stamped for that to compile.
		{"assets-empty-bundle", empty, "function main(): i32 {\n    var n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", 0},
		// Binary bytes: the length comes from the literal's own count word, so
		// an interior NUL is not a terminator.
		{"asset-binary", assets, "function main(): i32 {\n    return __fern_asset(\"z.txt\").len();\n}\n", 3},

		// Every refusal. Each of these is a program that would otherwise build
		// with a silently empty asset in it.
		{"no-embed-flag", "", "function main(): i32 {\n    return __fern_asset(\"a.txt\").len();\n}\n", -1},
		{"no-embed-flag-enumerate", "", "function main(): i32 {\n    var n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", -1},
		{"unknown-name", assets, "function main(): i32 {\n    return __fern_asset(\"a.tx\").len();\n}\n", -1},
		{"computed-name", assets, "function main(): i32 {\n    var n: string = \"a.txt\";\n    return __fern_asset(n).len();\n}\n", -1},
		{"wrong-arity", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\", \"z.txt\").len();\n}\n", -1},
		{"enumerate-with-argument", assets, "function main(): i32 {\n    var n: i32 = 0;\n    for a in __fern_assets(\"a.txt\") { n = n + 1; }\n    return n;\n}\n", -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, "embed_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			outDir := t.TempDir()

			nativeArgs := []string{"-target", "x86-64-linux", "-o", filepath.Join(outDir, "n")}
			shArgs := []string{"-target", "x86-64-linux", "-o", filepath.Join(outDir, "s")}
			if c.embedDir != "" {
				nativeArgs = append(nativeArgs, "-embed", c.embedDir)
				shArgs = append(shArgs, "-embed", c.embedDir)
			}
			nativeCmd := exec.Command(nativeBin, append(nativeArgs, src)...)
			nativeBuild, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, append(shArgs, src, stdlib)...)
			shBuild, _ := shCmd.CombinedOutput()

			nativeOK := nativeCmd.ProcessState.ExitCode() == 0
			shOK := shCmd.ProcessState.ExitCode() == 0
			if nativeOK != shOK {
				t.Fatalf("compilers disagree on whether this builds: native ok=%v, self-host ok=%v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeBuild, shBuild)
			}
			if c.wantExit < 0 {
				if nativeOK {
					t.Fatalf("both compilers accepted a program that must be refused\n--- native ---\n%s\n--- self-host ---\n%s", nativeBuild, shBuild)
				}
				return
			}
			if !nativeOK {
				t.Fatalf("both compilers refused a program that must build\n--- native ---\n%s\n--- self-host ---\n%s", nativeBuild, shBuild)
			}

			nativeRun := exec.Command(filepath.Join(outDir, "n"))
			nativeRunOut, _ := nativeRun.CombinedOutput()
			shRun := exec.Command(filepath.Join(outDir, "s"))
			shRunOut, _ := shRun.CombinedOutput()
			nativeCode := nativeRun.ProcessState.ExitCode()
			shCode := shRun.ProcessState.ExitCode()
			if nativeCode != c.wantExit {
				t.Errorf("native-compiled program exited %d, want %d\n%s", nativeCode, c.wantExit, nativeRunOut)
			}
			if shCode != c.wantExit {
				t.Errorf("self-host-compiled program exited %d, want %d\n%s", shCode, c.wantExit, shRunOut)
			}
		})
	}
}

// TestSelfHostEmbedCarriesTheStdlib is what #6643 actually needs: the whole
// internal/stdlib tree inside one module, reachable by name at runtime.
//
// A wasm-hosted compiler has no host filesystem to read the stdlib from, and
// the self-host CLI's stdlib root is a path on the command line — so until
// this works there is no way for the self-host compiler to ship the stdlib the
// way native's go:embed does. The size is asserted loosely, only that the
// bundle is actually IN the artifact: an embed that silently dropped the files
// would still enumerate zero and still exit 0 without the count check.
func TestSelfHostEmbedCarriesTheStdlib(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the self-host CLI; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stdlib embed check runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	// Counts the `.fern` modules and asserts one of them arrived intact, so a
	// walk that returned names without contents cannot pass.
	src := filepath.Join(dir, "embed_stdlib.fern")
	prog := "function main(): i32 {\n" +
		"    var n: i32 = 0;\n" +
		"    var io_len: i32 = 0;\n" +
		"    for a in __fern_assets() {\n" +
		"        if (a.0.len() > 5 && a.0[a.0.len() - 5] == b'.') { n = n + 1; }\n" +
		"        if (a.0 == \"std/io.fern\") { io_len = a.1.len(); }\n" +
		"    }\n" +
		"    if (n < 60) { return 1; }\n" +
		"    if (io_len < 100) { return 2; }\n" +
		"    return 0;\n" +
		"}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "stdemb")
	build := exec.Command(driverBin, "-target", "x86-64-linux", "-embed", stdlib, "-o", bin, src, stdlib)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("self-host could not embed the stdlib: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	out, _ := run.CombinedOutput()
	switch code := run.ProcessState.ExitCode(); code {
	case 0:
	case 1:
		t.Fatalf("fewer than 60 .fern modules reached the binary — the walk lost files\n%s", out)
	case 2:
		t.Fatalf("std/io.fern arrived with no contents — the walk kept names but not bytes\n%s", out)
	default:
		t.Fatalf("stdlib-embedding program exited %d\n%s", code, out)
	}
}

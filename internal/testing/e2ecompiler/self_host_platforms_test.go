package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostPlatformsCapabilityRules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("platforms_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/platforms_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/platforms_run.fern", "platforms_run")

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

func TestSelfHostCapabilityCorpusAgreesWithNative(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability corpus sweep runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	cases, err := filepath.Glob(filepath.Join("..", "..", "..", "conformance", "cases", "*", "main.fern"))
	if err != nil {
		t.Fatalf("globbing conformance cases: %v", err)
	}
	if len(cases) < 400 {
		t.Fatalf("found %d conformance cases, expected the full corpus — a silently shrunken sweep proves nothing", len(cases))
	}

	var diffs []string
	for _, c := range cases {
		nativeOut, _ := exec.Command(nativeBin, "-check", "-target", "wasm32-wasi", c).CombinedOutput()
		shOut, _ := exec.Command(driverBin, "-check", "-target", "wasm32-wasi", c, stdlib).CombinedOutput()
		want := strings.Join(e066Sites(string(nativeOut)), " ")
		got := strings.Join(e066Sites(string(shOut)), " ")
		if want != got {
			diffs = append(diffs, filepath.Base(filepath.Dir(c))+": native ["+want+"], self-host ["+got+"]")
		}
	}
	if len(diffs) > 0 {
		max := 15
		if len(diffs) < max {
			max = len(diffs)
		}
		t.Errorf("E066 disagreements on %d of %d conformance fixtures:\n  %s",
			len(diffs), len(cases), strings.Join(diffs[:max], "\n  "))
	}
}

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

func TestSelfHostTargetCapabilityDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range []struct {
		name   string
		target string
		src    string
	}{
		{"proc-fork-wasm", "wasm32-wasi", "function main(): i32 {\n    let pid: i32 = proc_fork();\n    return pid;\n}\n"},
		{"proc-fork-native-ok", "x86-64-linux", "function main(): i32 {\n    let pid: i32 = proc_fork();\n    return pid;\n}\n"},
		{"heap-mark-wasm", "wasm32-wasi", "function main(): i32 {\n    let m: i64 = __heap_mark();\n    __heap_release_to(m);\n    return 0;\n}\n"},
		{"heap-bump-bytes-wasm-ok", "wasm32-wasi", "function main(): i32 {\n    return __heap_bump_bytes();\n}\n"},
		{"subprocess-wasm", "wasm32-wasi", "function main(): i32 {\n    let argv: string[] = [];\n    let r: i32 = run_it(argv);\n    return r;\n}\nfunction run_it(argv: string[]): i32 {\n    subprocess(\"ls\", argv, \"\");\n    return 0;\n}\n"},
		{"subprocess-native-ok", "x86-64-linux", "function main(): i32 {\n    let argv: string[] = [];\n    let r: i32 = run_it(argv);\n    return r;\n}\nfunction run_it(argv: string[]): i32 {\n    subprocess(\"ls\", argv, \"\");\n    return 0;\n}\n"},
		{"fs-wasm-ok", "wasm32-wasi", "function main(): i32 {\n    match (read_file(\"x\")) {\n        Ok(s) => { return s.len(); },\n        Err(e) => { return 1; },\n    }\n    return 0;\n}\n"},
		{"print-wasm-ok", "wasm32-wasi", "function main(): i32 {\n    print(\"hi\");\n    return 0;\n}\n"},
		{"clock-wasm-ok", "wasm32-wasi", "function main(): i32 {\n    return (now_unix_ms() as i32);\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeOut, _ := exec.Command(nativeBin, "-check", "-target", c.target, src).CombinedOutput()
			shOut, _ := exec.Command(driverBin, "-check", "-target", c.target, src, stdlib).CombinedOutput()

			want := e066Sites(string(nativeOut))
			got := e066Sites(string(shOut))
			if strings.Join(want, " ") != strings.Join(got, " ") {
				t.Errorf("E066 sites differ: native %v, self-host %v\n--- native ---\n%s\n--- self-host ---\n%s",
					want, got, nativeOut, shOut)
			}
		})
	}
}

func TestSelfHostFreestandingTargets(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("freestanding target checks run only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	listing, _ := exec.Command(driverBin, "-targets").CombinedOutput()
	for _, want := range []string{"arm64-freestanding", "x86-64-freestanding"} {
		if !strings.Contains(string(listing), want) {
			t.Errorf("self-host -targets does not list %s:\n%s", want, listing)
		}
	}
	nativeListing, _ := exec.Command(nativeBin, "-targets").CombinedOutput()
	if string(listing) != string(nativeListing) {
		t.Errorf("-targets differs:\n--- native ---\n%s\n--- self-host ---\n%s", nativeListing, listing)
	}

	for _, c := range []struct {
		name   string
		target string
		src    string
	}{
		{"core-ok-arm64", "arm64-freestanding", "function main(): i32 { return 7; }\n"},
		{"core-ok-x86", "x86-64-freestanding", "function main(): i32 { return 7; }\n"},
		{"log-refused-arm64", "arm64-freestanding", "function main(): i32 {\n    print(\"x\");\n    return 0;\n}\n"},
		{"log-refused-x86", "x86-64-freestanding", "function main(): i32 {\n    print(\"x\");\n    return 0;\n}\n"},
		{"exit-ok", "arm64-freestanding", "function main(): i32 {\n    exit(0);\n    return 0;\n}\n"},
		{"unreachable-log-ok", "x86-64-freestanding", "function never_called(): void {\n    print(\"x\");\n}\nfunction main(): i32 { return 7; }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, "fs_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeCmd := exec.Command(nativeBin, "-check", "-target", c.target, src)
			nativeOut, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, "-check", "-target", c.target, src, stdlib)
			shOut, _ := shCmd.CombinedOutput()
			want := strings.Join(e066Sites(string(nativeOut)), " ")
			got := strings.Join(e066Sites(string(shOut)), " ")
			if want != got {
				t.Errorf("E066 sites differ: native [%s], self-host [%s]\n--- native ---\n%s\n--- self-host ---\n%s",
					want, got, nativeOut, shOut)
			}
			if wantCode, gotCode := nativeCmd.ProcessState.ExitCode(), shCmd.ProcessState.ExitCode(); wantCode != gotCode {
				t.Errorf("-check exit differs: native %d, self-host %d\n--- native ---\n%s\n--- self-host ---\n%s",
					wantCode, gotCode, nativeOut, shOut)
			}
		})
	}

	src := filepath.Join(dir, "fs_codegen.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, target := range []string{"arm64-freestanding", "x86-64-freestanding"} {
		t.Run("codegen-refused-"+target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			nativeCmd := exec.Command(nativeBin, "-target", target, "-o", out, src)
			nativeOut, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, "-target", target, "-o", out, src, stdlib)
			shOut, _ := shCmd.CombinedOutput()

			if nativeCmd.ProcessState.ExitCode() == 0 {
				t.Fatalf("native emitted for %s, which has no backend:\n%s", target, nativeOut)
			}
			if shCmd.ProcessState.ExitCode() == 0 {
				t.Errorf("self-host emitted for %s, which has no backend:\n%s", target, shOut)
			}
			if !strings.Contains(string(shOut), "no backend emits for this target") {
				t.Errorf("self-host refusal for %s does not say why:\n%s", target, shOut)
			}
		})
	}
}

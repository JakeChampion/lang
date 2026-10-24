package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const playgroundProgram = `import "std/i64";
function main(): i32 {
    let a: i64 = 48;
    let b: i64 = 36;
    return a.gcd(b) as i32;
}
`

func buildPlaygroundDriver(t *testing.T) string {
	t.Helper()
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	return buildPlaygroundDriverWith(t, stdlib)
}

func buildPlaygroundDriverWith(t *testing.T, stdlib string) string {
	t.Helper()
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("playground driver needs a supported native compiler host")
	}
	return buildPlaygroundDriverForTarget(t, stdlib, target)
}

func buildPlaygroundDriverForTarget(t *testing.T, stdlib, target string) string {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "playground_run.fern")
	bin := filepath.Join(dir, "playground_run")
	cmd := exec.Command(buildFernCLIBin(t), "-target", target,
		"-embed", stdlib, "-o", bin, filepath.Join(dir, "playground_run.fern"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the playground driver failed: %v\n%s", err, out)
	}
	return bin
}

func runPlayground(t *testing.T, bin, cwd, src string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(src)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if cmd.ProcessState == nil {
			t.Fatalf("start playground driver: %v", err)
		}
		if cmd.ProcessState.ExitCode() < 0 {
			errb.WriteString(err.Error() + "\n")
		}
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostPlaygroundOverlay(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	bin := buildPlaygroundDriver(t)
	t.Run("resolves-stdlib-from-the-overlay", func(t *testing.T) { playgroundResolves(t, bin) })
	t.Run("does-not-reach-the-disk", func(t *testing.T) { playgroundIgnoresDecoy(t, bin) })
	t.Run("reports-an-unresolvable-import", func(t *testing.T) { playgroundReportsMissing(t, bin) })
	t.Run("check-accepts-a-valid-program", func(t *testing.T) { playgroundCheckAccepts(t, bin) })
	t.Run("check-names-a-type-error", func(t *testing.T) { playgroundCheckRejects(t, bin) })
	t.Run("check-demangles-an-imported-struct", func(t *testing.T) { playgroundCheckDemangles(t, bin) })
	t.Run("check-derive-default-needs-a-field-default", func(t *testing.T) { playgroundCheckDeriveDefault(t, bin) })
	t.Run("interp-runs-and-prints", func(t *testing.T) { playgroundInterpRuns(t, bin) })
	t.Run("interp-checks-first", func(t *testing.T) { playgroundInterpChecksFirst(t, bin) })
	t.Run("interp-reaches-the-stdlib", func(t *testing.T) { playgroundInterpStdlib(t, bin) })
	t.Run("interp-maps-the-exit-code", func(t *testing.T) { playgroundInterpExit(t, bin) })
	t.Run("interp-dispatches-byte-methods", func(t *testing.T) { playgroundInterpByteMethods(t, bin) })
	t.Run("lsp-publishes-diagnostics", func(t *testing.T) { playgroundLSPDiagnostics(t, bin) })
	t.Run("lsp-answers-from-the-stdlib", func(t *testing.T) { playgroundLSPStdlibAnswers(t, bin) })
}

const playgroundLSPURI = "file:///playground.fern"

const playgroundLSPSource = "import \"std/i32\";\nfunction main(): i32 {\n    let f: i32 = 5;\n    print(f.to_string());\n    let y: i32 = \"s\";\n    return f;\n}\n"

func playgroundLSPSession(t *testing.T, asks ...lspMsg) string {
	t.Helper()
	var in []byte
	for _, m := range session(append([]lspMsg{lspOpen(playgroundLSPURI, playgroundLSPSource)}, asks...)...) {
		in = append(in, lspFrameBytes(t, m)...)
	}
	return string(in)
}

func lspAt(id int, method string, line, char int) lspMsg {
	return lspMsg{"id": id, "method": method, "params": lspMsg{"textDocument": lspMsg{"uri": playgroundLSPURI}, "position": lspMsg{"line": line, "character": char}}}
}

func playgroundLSPDiagnostics(t *testing.T, bin string) {
	stdout, stderr, code := runPlayground(t, bin, t.TempDir(), playgroundLSPSession(t), "-lsp")
	if code != 0 {
		t.Fatalf("-lsp exited %d\n%s", code, stderr)
	}
	want := []lspPublish{{URI: playgroundLSPURI, Diagnostics: []lspDiag{{
		Range: span(4, 4, 4, 5), Severity: 1, Source: "fern", Code: "E003", Message: `variable "y": expected i32, got string`,
	}}}}
	if got := publishes(t, parseLSPFrames(t, []byte(stdout))); !reflect.DeepEqual(got, want) {
		t.Errorf("-lsp published %+v\nwant %+v", got, want)
	}
}

func playgroundLSPStdlibAnswers(t *testing.T, bin string) {
	stdout, stderr, code := runPlayground(t, bin, t.TempDir(), playgroundLSPSession(t,
		lspAt(2, "textDocument/hover", 3, 13), lspAt(3, "textDocument/completion", 3, 12),
		lspAt(4, "textDocument/definition", 3, 13)), "-lsp")
	if code != 0 {
		t.Fatalf("-lsp exited %d\n%s", code, stderr)
	}
	frames := parseLSPFrames(t, []byte(stdout))
	var hover struct {
		Contents struct{ Value string } `json:"contents"`
		Range    lspRange               `json:"range"`
	}
	if err := json.Unmarshal(response(t, frames, "2").Result, &hover); err != nil {
		t.Fatal(err)
	}
	if want := "```fern\n(method on i32) function to_string(): string\n```\n\nThe decimal text of `n`, with a leading `-` when it is negative."; hover.Contents.Value != want || hover.Range != span(3, 12, 3, 21) {
		t.Errorf("hover = %+v, want %q at 3:12-3:21", hover, want)
	}
	var completion struct {
		Items []struct{ Label, Detail string } `json:"items"`
	}
	if err := json.Unmarshal(response(t, frames, "3").Result, &completion); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range completion.Items {
		found = found || it.Label == "to_string" && it.Detail == "function to_string(): string"
	}
	if !found {
		t.Errorf("completion after `f.` offered %d items, none of them std/i32's to_string: %+v", len(completion.Items), completion.Items)
	}
	var def lspLocation
	if err := json.Unmarshal(response(t, frames, "4").Result, &def); err != nil {
		t.Fatal(err)
	}
	if def.URI != "file:///stdlib/std/i32.fern.md" || def.Range.Start.Line == 0 {
		t.Errorf("definition of to_string = %+v, want a line of file:///stdlib/std/i32.fern.md", def)
	}
}

func playgroundInterpByteMethods(t *testing.T, bin string) {
	const src = `import "std/i32";
function main(): i32 {
    let s: string = "a1";
    let n: i32 = 0;
    if (s[1].is_ascii_digit()) { n = n + 1; }
    if (s[0].is_ascii_alpha()) { n = n + 2; }
    let b: i32 = 32;
    if ((b as u8).is_ascii_white_space()) { n = n + 4; }
    return n;
}
`
	_, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-interp")
	if code != 7 {
		t.Fatalf("-interp exited %d, want 7 (native's answer)\n%s", code, stderr)
	}
}

func playgroundInterpRuns(t *testing.T, bin string) {
	out, stderr, code := runPlayground(t, bin, t.TempDir(),
		"function main(): i32 {\n  print(\"hi\");\n  return 7;\n}\n", "-interp")
	if code != 7 {
		t.Fatalf("-interp exited %d, want the program's own 7\n%s", code, stderr)
	}
	if out != "hi\n" {
		t.Errorf("stdout = %q, want %q — the program's output, not a module", out, "hi\n")
	}
}

func playgroundInterpStdlib(t *testing.T, bin string) {
	const src = `import "std/i32";
function main(): i32 {
  let n: i32 = 42;
  print("n=" + n.to_string());
  print(f"fstring {n}");
  return 0;
}
`
	out, stderr, code := runPlayground(t, bin, t.TempDir(), src, "-interp")
	if code != 0 {
		t.Fatalf("-interp exited %d on a std/i32 program, want 0\n%s", code, stderr)
	}
	if want := "n=42\nfstring 42\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func playgroundInterpExit(t *testing.T, bin string) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"i32-result", "function main(): i32 { return 12; }\n", 12},
		{"boolean-true", "function main(): boolean { return true; }\n", 1},
		{"boolean-false", "function main(): boolean { return false; }\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runPlayground(t, bin, t.TempDir(), tc.src, "-interp")
			if code != tc.want {
				t.Errorf("-interp exited %d, want %d\n%s", code, tc.want, stderr)
			}
		})
	}
}

func playgroundCheckAccepts(t *testing.T, bin string) {
	out, stderr, code := runPlayground(t, bin, t.TempDir(), playgroundProgram, "-check")
	if code != 0 {
		t.Fatalf("-check rejected a valid program: exit %d\n%s", code, stderr)
	}
	if out != "" {
		t.Errorf("-check wrote %d bytes to stdout; it must emit nothing:\n%s", len(out), out)
	}
	if stderr != "" {
		t.Errorf("-check reported a diagnostic on a valid program:\n%s", stderr)
	}
}

func playgroundInterpChecksFirst(t *testing.T, bin string) {
	out, stderr, code := runPlayground(t, bin, t.TempDir(),
		"function main(): i32 {\n  let s: string = \"ab\";\n  print(\"x\" + s[0:1]);\n  return 0;\n}\n", "-interp")
	if code != 1 {
		t.Errorf("-interp on a type error exited %d, want 1", code)
	}
	if out != "" {
		t.Errorf("-interp ran a program that does not type-check; it printed:\n%s", out)
	}
	if !strings.Contains(stderr, "E009") || strings.Contains(stderr, "fern: interp:") {
		t.Errorf("-interp did not report the type error as a diagnostic:\n%s", stderr)
	}
}

func playgroundCheckDemangles(t *testing.T, bin string) {
	_, stderr, code := runPlayground(t, bin, t.TempDir(),
		"import \"core/iter\" as it;\nfunction main(): i32 { let total: i32 = it.of([1, 2, 3]).sum(); return total; }\n", "-check")
	if code == 0 {
		t.Fatal("-check accepted a method ArrayIter does not have")
	}
	if !strings.Contains(stderr, `struct iter.ArrayIter has no field or method "sum"`) || strings.Contains(stderr, "__") {
		t.Errorf("the diagnostic does not name iter.ArrayIter:\n%s", stderr)
	}
}

func playgroundCheckDeriveDefault(t *testing.T, bin string) {
	_, stderr, code := runPlayground(t, bin, t.TempDir(),
		"import \"core/cmp\";\nstruct Inner { v: i32 }\n@derive(cmp.Default)\nstruct Outer { i: Inner, n: i32 }\nfunction main(): i32 { return 0; }\n", "-check")
	if code == 0 {
		t.Fatal("-check accepted a Default derive over a field with no default")
	}
	if !strings.Contains(stderr, "4:1") || !strings.Contains(stderr, "E021") || !strings.Contains(stderr, "field i of type Inner") || strings.Contains(stderr, "E001") {
		t.Errorf("want E021 at the derive naming field i:\n%s", stderr)
	}
}

func playgroundCheckRejects(t *testing.T, bin string) {
	out, stderr, code := runPlayground(t, bin, t.TempDir(),
		"function main(): i32 { return nosuchthing; }\n", "-check")
	if code == 0 {
		t.Fatal("-check accepted a program with an undefined name")
	}
	if out != "" {
		t.Errorf("-check wrote %d bytes to stdout on a failing program:\n%s", len(out), out)
	}
	for _, want := range []string{"1:31", "E001", "nosuchthing"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the diagnostic does not carry %q:\n%s", want, stderr)
		}
	}
}

func playgroundResolves(t *testing.T, bin string) {
	empty := t.TempDir()
	wat, stderr, code := runPlayground(t, bin, empty, playgroundProgram, "-emit", "asm")
	if code != 0 {
		t.Fatalf("driver exited %d in an empty directory, want 0\n%s", code, stderr)
	}
	if !strings.HasPrefix(wat, "(module") {
		t.Fatalf("driver did not emit a module:\n%s\n%s", wat, stderr)
	}
	assertWatRuns(t, wat, 12)
}

func playgroundIgnoresDecoy(t *testing.T, bin string) {
	decoy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(decoy, "std"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "std", "i64.fern"),
		[]byte("pub function gcd(a: i64, b: i64): i64 { return 999; }\n"), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	wat, stderr, code := runPlayground(t, bin, decoy, playgroundProgram, "-emit", "asm")
	if code != 0 {
		t.Fatalf("driver exited %d, want 0\n%s", code, stderr)
	}
	assertWatRuns(t, wat, 12)
}

func playgroundReportsMissing(t *testing.T, bin string) {
	_, stderr, code := runPlayground(t, bin, t.TempDir(),
		"import \"std/nosuchmodule\";\nfunction main(): i32 { return 0; }\n")
	if code == 0 {
		t.Fatalf("driver accepted a program importing a module it cannot resolve")
	}
	if want := `playground: stdin:1:1: error[E086]: unknown stdlib module "std/nosuchmodule"`; !strings.Contains(stderr, want) {
		t.Errorf("the diagnostic does not place the import:\n%s\nwant %s", stderr, want)
	}
}

func TestSelfHostPlaygroundHostedInWasm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the playground driver to wasm; skipped under -short")
	}
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	for _, tool := range []string{"wasmtime", "wasm-tools"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "playground_run.fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	mod := filepath.Join(dir, "playground.wasm")
	build := exec.Command(buildFernCLIBin(t), "-target", "wasm32-wasi", "-emit", "core-module",
		"-embed", stdlib, "-o", mod, filepath.Join(dir, "playground_run.fern"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the playground driver for wasm failed: %v\n%s", err, out)
	}

	run := exec.Command("wasmtime", "run", "--invoke", "main", mod, "-emit", "asm")
	run.Dir = t.TempDir()
	run.Stdin = strings.NewReader(playgroundProgram)
	var out strings.Builder
	run.Stdout = &out
	if err := run.Run(); err != nil {
		t.Fatalf("wasm-hosted driver failed: %v", err)
	}
	wat := strings.TrimSuffix(out.String(), "0\n")
	if !strings.HasPrefix(wat, "(module") {
		t.Fatalf("wasm-hosted driver emitted no module:\n%q", out.String())
	}
	assertWatRuns(t, wat, 12)

	native := buildPlaygroundDriver(t)
	nativeWat, _, code := runPlayground(t, native, t.TempDir(), playgroundProgram, "-emit", "asm")
	if code != 0 {
		t.Fatalf("natively-hosted driver exited %d", code)
	}
	if wat != nativeWat {
		t.Errorf("wasm-hosted and natively-hosted output differ: %d vs %d bytes", len(wat), len(nativeWat))
	}

	t.Run("literate-standard-library", func(t *testing.T) {
		for _, mode := range []struct{ flag, want string }{{"-check", "0"}, {"-interp", "42"}} {
			cmd := exec.Command("wasmtime", "run", "--invoke", "main", mod, mode.flag)
			cmd.Dir = t.TempDir()
			cmd.Stdin = strings.NewReader(literateStdlibProgram)
			if output, err := cmd.Output(); err != nil || strings.TrimSpace(string(output)) != mode.want {
				t.Fatalf("%s: %v\n%s", mode.flag, err, output)
			}
		}
	})

	chk := exec.Command("wasmtime", "run", "--invoke", "main", mod, "-check")
	chk.Dir = t.TempDir()
	chk.Stdin = strings.NewReader("function main(): i32 { return nosuchthing; }\n")
	var chkOut, chkErr strings.Builder
	chk.Stdout = &chkOut
	chk.Stderr = &chkErr
	if err := chk.Run(); err != nil {
		t.Fatalf("wasm-hosted -check failed: %v\n%s", err, chkErr.String())
	}
	if got := strings.TrimSpace(chkOut.String()); got != "1" {
		t.Errorf("wasm-hosted -check returned %q, want \"1\" on a program with an undefined name", got)
	}
	if !strings.Contains(chkErr.String(), "E001") {
		t.Errorf("wasm-hosted -check did not name the error:\n%s", chkErr.String())
	}
	if strings.Contains(chkErr.String(), "over-release") {
		t.Errorf("wasm-hosted -check reported an rc over-release (#7969):\n%s", chkErr.String())
	}

	in := playgroundLSPSession(t, lspAt(2, "textDocument/hover", 3, 13))
	srv := exec.Command("wasmtime", "run", "--invoke", "main", mod, "-lsp")
	srv.Dir = t.TempDir()
	srv.Stdin = strings.NewReader(in)
	var srvOut, srvErr strings.Builder
	srv.Stdout = &srvOut
	srv.Stderr = &srvErr
	if err := srv.Run(); err != nil {
		t.Fatalf("wasm-hosted -lsp failed: %v\n%s", err, srvErr.String())
	}
	nativeSession, _, _ := runPlayground(t, native, t.TempDir(), in, "-lsp")
	if got := strings.TrimSuffix(srvOut.String(), "0\n"); got != nativeSession {
		t.Errorf("wasm-hosted -lsp answered\n%s\nnatively-hosted\n%s", got, nativeSession)
	}

	usizeSrc, usizeWant := usizeCase(t, "usize_wraps_at_32_bits")
	usizeProg, err := os.ReadFile(usizeSrc)
	if err != nil {
		t.Fatal(err)
	}
	ip := exec.Command("wasmtime", "run", "--invoke", "main", mod, "-interp")
	ip.Dir = t.TempDir()
	ip.Stdin = strings.NewReader(string(usizeProg))
	var ipOut, ipErr strings.Builder
	ip.Stdout = &ipOut
	ip.Stderr = &ipErr
	if err := ip.Run(); err != nil {
		t.Fatalf("wasm-hosted -interp failed: %v\n%s", err, ipErr.String())
	}
	if got := strings.TrimSuffix(ipOut.String(), "0\n"); got != usizeWant {
		t.Errorf("wasm-hosted -interp printed\n%s\nwant\n%s\nstderr:\n%s", got, usizeWant, ipErr.String())
	}
}

func assertWatRuns(t *testing.T, wat string, want int) {
	t.Helper()
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "out.wat")
	binPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("emitted WAT does not parse: %v\n%s", err, out)
	}
	cmd := exec.Command("wasmtime", "run", binPath)
	out, _ := cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("compiled program exited %d, want %d\n%s", got, want, out)
	}
}

func TestSelfHostPlaygroundDiagnosticNamesItsFile(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "std", "bad.fern"), []byte("pub function bad(): i32 {\n    return \"x\";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildPlaygroundDriverWith(t, root)
	_, stderr, code := runPlayground(t, bin, t.TempDir(), "import \"std/bad\";\nfunction main(): i32 { return bad.bad(); }\n", "-check")
	if code == 0 {
		t.Fatalf("-check accepted a program whose import does not type-check:\n%s", stderr)
	}
	if want := "stdlib://std/bad.fern:2:5: error[E002]"; !strings.Contains(stderr, want) {
		t.Errorf("-check did not name the module: want %q in\n%s", want, stderr)
	}
}

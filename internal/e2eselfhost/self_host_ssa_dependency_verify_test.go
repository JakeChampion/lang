package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSSADependencyVerificationIRArm64(t *testing.T) {
	testDependencyVerificationIR(t, "arm64-linux")
}
func TestSelfHostSSADependencyVerificationIRX86_64(t *testing.T) {
	testDependencyVerificationIR(t, "x86-64-linux")
}
func TestSelfHostSSADependencyVerificationIRWasm(t *testing.T) {
	testDependencyVerificationIR(t, "wasm32-wasi")
}

func testDependencyVerificationIR(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	var armGCC, armRunner, wasmtime string
	if target == "arm64-linux" {
		armGCC, armRunner = arm64Tooling(t)
	}
	if target == "wasm32-wasi" {
		var err error
		wasmtime, err = exec.LookPath("wasmtime")
		if err != nil {
			t.Skip("wasmtime not on PATH")
		}
	}
	dir := copySelfHostTree(t)
	copySelfHostDriver(t, dir, "ssadeps.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	// One compiled program exercises every verdict, avoiding one whole-module
	// compiler build per small metadata mutation.
	var source, want, main strings.Builder
	source.WriteString("import \"./ssa\";\nimport \"./ssalive\";\nimport \"./ssadeps\";\n")
	main.WriteString("function main(): i32 {\n")
	for i, tc := range dependencyVerifyCases() {
		src := dependencyVerifySource(t, tc)
		at := strings.Index(src, "function main()")
		if at < 0 {
			t.Fatal("fixture has no main function")
		}
		source.WriteString(strings.Replace(src[at:], "function main()", fmt.Sprintf("function dependency_case_%d()", i), 1))
		fmt.Fprintf(&main, "if (dependency_case_%d() != 0) { return %d; }\n", i, i+1)
		want.WriteString(tc.want + "\n")
	}
	main.WriteString("return 0;\n}\n")
	source.WriteString(main.String())
	entry := filepath.Join(dir, "dependency_fixture.fern")
	if err := os.WriteFile(entry, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, driver, "-target", target, "-emit", "asm", entry, root)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		t.Fatalf("self-host compile: %v\n%s", err, diagnostics.String())
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		run = runX86_64Bin(runner, buildBin(t, gcc, dir, "verify", string(output)))
	case "arm64-linux":
		run = runArm64Bin(armRunner, buildBinArm64(t, armGCC, dir, "verify", string(output)))
	case "wasm32-wasi":
		wat := filepath.Join(dir, "verify.wat")
		if err := os.WriteFile(wat, output, 0o644); err != nil {
			t.Fatal(err)
		}
		run = exec.Command(wasmtime, "run", wat)
	}
	got, err := run.CombinedOutput()
	if err != nil || string(got) != want.String() {
		t.Fatalf("dependency verification: %v\ngot %q\nwant %q", err, got, want.String())
	}
}

type dependencyVerifyCase struct{ name, graph, change, want string }

func dependencyVerifyCases() []dependencyVerifyCase {
	return []dependencyVerifyCase{
		{"nested-projection", "nested-projection", "", ""},
		{"phi-edges", "phi-edges", "", ""},
		{"loop-anchor", "loop-anchor", "", ""},
		{"rebound-value", "rebound-value", "", ""},
		{"missing-transitive-root", "nested-projection", "deps = deps.with(2, [1]);", "incomplete dependency closure"},
		{"self-dependency", "nested-projection", "deps = deps.with(0, [0]);", "cyclic dependency"},
		{"cycle", "nested-projection", "deps = deps.with(0, [1]);", "cyclic dependency"},
		{"wrong-phi-edge", "phi-edges", "deps = deps.with(2, [3]);", "dependency unavailable at use"},
		{"branch-root-at-join", "phi-edges", "deps = deps.with(5, [1]);", "dependency unavailable at use"},
		{"forward-local-root", "nested-projection", "deps = deps.with(0, [1]); deps = deps.with(1, []); deps = deps.with(2, [1]);", "dependency unavailable at use"},
		{"duplicate-definition", "nested-projection", "var b = f.blocks[0]; b = ssa.SBlock { ...b, insts: b.insts.append(b.insts[0]) }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "duplicate value definition"},
		{"undefined-operand", "nested-projection", "var b = f.blocks[0]; b = ssa.SBlock { ...b, insts: [b.insts[1], b.insts[2]] }; f = ssa.SFunc { ...f, blocks: f.blocks.with(0, b) };", "undefined value"},
		{"phi-after-use", "phi-edges", "var b = f.blocks[3]; var ins = ssa.SInst { kind_tag: 1, result: 0 - 1, args: [], imm: 0, str: \"\" }; b = ssa.SBlock { ...b, insts: [ins, b.insts[0]] }; f = ssa.SFunc { ...f, blocks: f.blocks.with(3, b) };", "invalid phi definition"},
	}
}

func dependencyVerifySource(t *testing.T, tc dependencyVerifyCase) string {
	t.Helper()
	source, _ := lifetimeFernFixture(t, selfHostLifetimeFixtures()[tc.graph])
	source = strings.Replace(source, "import \"./ssalive\";", "import \"./ssalive\";\nimport \"./ssadeps\";", 1)
	start := strings.Index(source, "var before =")
	if start < 0 {
		t.Fatal("fixture has no execution boundary")
	}
	return source[:start] + tc.change + `
var before = ssa.print_func(f);
var checked = ssadeps.analyze(f, deps);
var err = checked.why;
if (checked.ok != (err == "")) { return 3; }
if (!checked.ok && (checked.live_in.len() != 0 || checked.live_out.len() != 0)) { return 4; }
if (ssa.print_func(f) != before) { return 2; }
print(err);
return 0;
}
`
}

func TestSelfHostSSADependencyVerification(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	for _, tc := range dependencyVerifyCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "ssadeps.fern")
			source := dependencyVerifySource(t, tc)
			if err := os.WriteFile(filepath.Join(dir, "dependency_fixture.fern"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := buildSelfHostBin(t, gcc, dir, "dependency_fixture.fern", "dependencies")
			argv := append(append([]string{}, runner...), bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			got, err := cmd.CombinedOutput()
			if err != nil || string(got) != tc.want+"\n" {
				t.Fatalf("dependency verification: %v\ngot %s\nwant %s", err, got, tc.want)
			}
		})
	}
}

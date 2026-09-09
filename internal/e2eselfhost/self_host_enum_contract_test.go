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

func enumContractRuntimeCases() []arrayClaimCase {
	var cases []arrayClaimCase
	for _, body := range []struct{ name, body string }{
		{"direct", "var src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { return xs; }, Empty => { return [0]; } }"},
		{"alias", "var src: E = E.Full([i, i + 1]); var x: E = src; var y: E = x; match (y) { Full(xs) => { return xs; }, Empty => { return [0]; } }"},
		{"payload-alias", "var src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { var ys = xs; return ys; }, Empty => { return [0]; } }"},
		{"conditional", "var src: E = E.Full([i, i + 1]); match (src) { Full(xs) => { if (i % 2 == 0) { return xs; } }, Empty => {} } return [i, i + 1];"},
		{"shadowed-parameter", "if (i >= 0) { var i: E = E.Full([i, i + 1]); match (i) { Full(xs) => { return xs; }, Empty => { return [0]; } } } return [0, 1];"},
	} {
		cases = append(cases, arrayClaimCase{body.name, `enum E { Full(i32[]), Empty }
@noinline function produce(i: i32): i32[] { ` + body.body + ` }
@noinline function exercise(i: i32): i32 {
    var xs = produce(i); var churn = [91, 92];
    if (xs.len() != 2 || xs[0] != i || xs[1] != i + 1 || churn[0] != 91) { return 2; }
    return 0;
}
function main(): i32 {
    var i = 0; while (i < 32) { var r = exercise(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	}
	cases = append(cases, arrayClaimCase{"shared-child", `enum E { Full(i32[]), Empty }
@noinline function take(e: E): i32[] {
    match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } }
}
@noinline function exercise(i: i32): i32 {
    var e = E.Full([i, i + 1]); var alias: E = e;
    var first = take(e); var second = take(alias); var churn = [91, 92];
    if (first[0] != i || second[1] != i + 1 || churn[0] != 91) { return 2; }
    match (e) { Full(xs) => { if (xs[0] != i || xs[1] != i + 1) { return 3; } }, Empty => { return 4; } }
    return 0;
}
function main(): i32 {
    var i = 0; while (i < 32) { var r = exercise(i); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	cases = append(cases, arrayClaimCase{"empty-variant", `enum E { Full(i32[]), Empty }
@noinline function take(e: E): i32[] {
    match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } }
}
@noinline function exercise(): i32 {
    var e = E.Empty; var xs = take(e); var churn = [91];
    if (xs.len() != 1 || xs[0] != 0 || churn[0] != 91) { return 2; } return 0;
}
function main(): i32 {
    var i = 0; while (i < 32) { var r = exercise(); if (r != 0) { return r; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; } return 0;
}`, true})
	return cases
}

func TestSelfHostEnumContractVerify(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	var checks strings.Builder
	for i, tc := range []struct {
		body string
		ok   bool
	}{
		{`match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } }`, true},
		{`var alias: E = e; match (alias) { Full(xs) => { return xs; }, Empty => { return [0]; } }`, true},
		{`match (e) { Full(xs) => { var ys = xs; return ys; }, Empty => { return [0]; } }`, true},
		{`e = E.Full([1]); return [0];`, false},
		{`var f = (): i32 => { match (e) { Full(xs) => { return xs[0]; }, Empty => { return 0; } } }; return [0];`, false},
		{`return unknown(e);`, false},
		{`while (true) { return [0]; }`, false},
		{`var xs = [1]; var local = E.Full(xs); return xs;`, false},
	} {
		src := `enum E { Full(i32[]), Empty } function take(e: E): i32[] { ` + tc.body + ` }`
		fmt.Fprintf(&checks, "if (accept(%q) != %t) { return %d; }\n", src, tc.ok, i+1)
	}
	source := `import "./enumcontract"; import "./parser"; import "./lexer"; import "./typeinfo";
function region(): enumcontract.Region {
    var mod = parser.parse_module(lexer.tokenize("enum E { Full(i32[]), Empty } function take(e: E): i32[] { match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } } }"));
    return enumcontract.import_function(mod.funcs[0], mod.structs);
}

function accept(src: string): boolean {
    var mod = parser.parse_module(lexer.tokenize(src));
    return enumcontract.analyze(mod.funcs[0], mod.structs).ok;
}
function main(): i32 {
` + checks.String() + `
    var r = region(); var p = enumcontract.verify(r);
    if (!p.ok || p.borrowed.len() != 1 || !p.borrowed[0] || p.roots.len() != 0) { return 20; }
    var effects: enumcontract.Effect[] = [];
    for e in r.effects { if (e.kind != 2 && e.kind != 3) { effects = effects.append(e); } }
    if (enumcontract.verify(enumcontract.Region { ...r, effects: effects }).ok) { return 21; }
    var values = r.values;
    var i = 0;
    while (i < values.len()) {
        if (values[i].kind == 5) { values = values.with(i, enumcontract.Value { ...values[i], field: 99 }); }
        i = i + 1;
    }
    if (enumcontract.verify(enumcontract.Region { ...r, values: values }).ok) { return 22; }
    if (enumcontract.verify(enumcontract.Region { ...r, params: [99] }).ok) { return 23; }
    if (enumcontract.verify(enumcontract.Region { ...r, params: [0, 0] }).ok) { return 28; }
    var mod = parser.parse_module(lexer.tokenize("enum E { Full(i32[]), Empty } function take(e: E): i32[] { match (e) { Full(xs) => { return xs; }, Empty => { return [0]; } } } function caller(i: i32): i32 { var e = E.Full([i]); var xs = take(e); return xs[0]; }"));
    var leaves = enumcontract.leaves(mod.funcs, mod.structs);
    var caller = enumcontract.analyze_calls(mod.funcs[1], mod.structs, leaves);
    if (!caller.ok || caller.roots.len() != 1) { return 24; }
    var invalid = enumcontract.Region { ...leaves[0], params: [99] };
    if (enumcontract.analyze_calls(mod.funcs[1], mod.structs, [invalid]).ok) { return 25; }
    invalid = enumcontract.Region { ...leaves[0], result: typeinfo.TypeBool { tag: 0 } };
    if (enumcontract.analyze_calls(mod.funcs[1], mod.structs, [invalid]).ok) { return 26; }
    invalid = enumcontract.Region { ...leaves[0], callees: leaves };
    if (enumcontract.analyze_calls(mod.funcs[1], mod.structs, [invalid]).ok) { return 27; }
    return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "enum-contract.fern"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "enum-contract.fern", "contract")
	if output, err := runX86_64Bin(runner, driver).CombinedOutput(); err != nil {
		t.Fatalf("typed contract: %v\n%s", err, output)
	}
}

func TestSelfHostEnumContractMissingRetainX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	path := filepath.Join(dir, "irlower.fern")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, retain := range []string{
		`rbl = rbl.retain(rps, "ret-borrowed-binding");`,
		`rbl = rbl.retain_tos("ret-borrowed-binding");`,
	} {
		if strings.Count(source, retain) != 1 {
			t.Fatalf("mutation target changed: %s", retain)
		}
		source = strings.Replace(source, retain, "// deliberately omitted by the lifetime negative control", 1)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "mutated")
	asm := hevCompile(t, runner, driver, enumContractRuntimeCases()[1].source, []string{"FERN_LEAKCHECK=1"})
	output, code := hevRun(t, runner, buildBin(t, gcc, dir, "missing-retain", asm))
	if code != 99 {
		t.Fatalf("missing retain: want post-frame underflow exit 99, got %d\n%s", code, output)
	}
}

func TestSelfHostEnumContractIRArm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	armgcc, armrunner := arm64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	stage0 := buildSelfHostBin(t, gcc, dir, "fern.fern", "stage0")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	build := runX86_64Bin(runner, stage0, "-target", "arm64-linux", "-emit", "asm", filepath.Join(dir, "fern.fern"), root)
	var diagnostic bytes.Buffer
	build.Stderr = &diagnostic
	asm, err := build.Output()
	if err != nil {
		t.Fatalf("self-host compiler build: %v\n%s", err, diagnostic.String())
	}
	stage1 := buildBinArm64(t, armgcc, dir, "stage1", string(asm))
	for _, tc := range enumContractRuntimeCases() {
		for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				input := filepath.Join(dir, "input.fern")
				if err := os.WriteFile(input, []byte(tc.source), 0o644); err != nil {
					t.Fatal(err)
				}
				emitTarget, mode := target, "FERN_LEAKCHECK=1"
				if target == "x86-64-sanitize" {
					emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
				}
				compile := runArm64Bin(armrunner, stage1, "-target", emitTarget, "-emit", "asm", input, root)
				compile.Env = append(os.Environ(), mode)
				var stderr bytes.Buffer
				compile.Stderr = &stderr
				output, err := compile.Output()
				if err != nil {
					t.Fatalf("self-host compilation: %v\n%s", err, stderr.String())
				}
				var run *exec.Cmd
				switch target {
				case "arm64-linux":
					run = runArm64Bin(armrunner, buildBinArm64(t, armgcc, dir, "program", string(output)))
				case "x86-64-linux", "x86-64-sanitize":
					run = runX86_64Bin(runner, buildBin(t, gcc, dir, "program", string(output)))
				case "wasm32-wasi":
					wat := filepath.Join(dir, "program.wat")
					if err := os.WriteFile(wat, output, 0o644); err != nil {
						t.Fatal(err)
					}
					run = exec.Command("wasmtime", "run", wat)
				}
				got, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("self-host runtime: %v\n%s", err, got)
				}
				if target != "wasm32-wasi" {
					var allocs, frees, live int64
					summary := leakSummaryLine(string(got))
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatal(err)
					}
					if allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("unbalanced: %s", summary)
					}
				}
			})
		}
	}
}

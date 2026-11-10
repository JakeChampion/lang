package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func checkedOwnedResultCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const prefix = `struct Box { values: u8[] }
function take(own box: Box): Box { return box; }
function (own box: Box) close(): void {}
`
	for _, tc := range []struct{ name, body, code string }{
		{"fresh scalar field", `function fresh(x: Box): own Box { return Box { values: [x.values.len() as u8] }; }`, ""},
		{"checked value copy", `@value struct Key { bytes: u8[] } function (own k: Key) erase(): void {} function copy(k: Key): own Key { return k; }`, ""},
		{"method result", `function (x: Box) fresh(): own Box { return Box { values: [] }; } function pass(x: Box): own Box { return x.fresh(); }`, ""},
		{"conditional result", `function pass[T](own x: T, b: boolean): own T { return if (b) { x } else { x }; }`, ""},
		{"match result", `function pass[T](own x: T, b: boolean): own T { return match (b) { true => x, false => x }; }`, ""},
		{"local function capture", `function run(own x: Box): void { function f(): own Box { return x; } }`, "E051"},
		{"borrowed conditional", `function pass(x: Box, b: boolean): own Box { return if (b) { x } else { Box { values: [] } }; }`, "E051"},
		{"bound factory", `trait Factory { function make(): own Self; } impl Factory for Box { function make(): own Self { return Box { values: [] }; } } function make[T: Factory](): own T { return T.make(); }`, ""},
		{"trait promise", `trait Factory { function make(): own Self; } impl Factory for Box { function make(): Self { return Box { values: [] }; } }`, "E021"},
		{"copied array element", `struct Data { bytes: u8[] } function wrap(data: Data): own Data[] { return [data]; }`, ""},
		{"copied tuple field", `struct Data { bytes: u8[] } function wrap(data: Data): own (Data, i32) { return (data, 1); }`, ""},
		{"copied record field", `struct Data { bytes: u8[] } struct Outer { data: Data } function wrap(data: Data): own Outer { return Outer { data: data }; }`, ""},
		{"copied captured element", `struct Data { bytes: u8[] } function wrap(data: Data): void { let f = (): own Data[] => [data]; }`, ""},
		{"fresh local", `function make(): own Box { let box = Box { values: [] }; return box; }`, ""},
		{"fresh lambda local", `function run(): void { let f = (): own Box => { let box = Box { values: [] }; return box; }; }`, ""},
		{"owned field", `struct Outer { box: Box } function get(own outer: Outer): own Box { return outer.box; }`, ""},
		{"borrowed local alias", `function bad(box: Box): own Box { let local = box; return local; }`, "E051"},
		{"captured local", `function make(): own Box { let box = Box { values: [] }; let f = (): Box => box; return box; }`, "E051"},
		{"deferred field", `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(own o: Outer): own Box { defer o.close(); return o.box; }`, "E051"},
		{"deferred sibling", `struct Pair { first: Box, second: Box } function get(own p: Pair): own Box { defer p.first.close(); return p.second; }`, ""},
		{"deferred local root", `function get(): own Box { let b = Box { values: [] }; defer b.close(); return b; }`, "E051"},
		{"deferred local field", `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(): own Box { let o = Outer { box: Box { values: [] } }; defer o.close(); return o.box; }`, "E051"},
		{"generic input", `function pass[T](own x: T): own T { return x; }`, ""},
		{"callback result", `function pass[T](own x: T, f: (own T) => own T): own T { return f(x); }`, ""},
		{"lambda result", `function run(): void { let f: (own Box) => own Box = (own x: Box): own Box => x; }`, ""},
		{"weaken result", `function pass(own x: Box): own Box { return x; } function run(): void { let f: (own Box) => Box = pass; }`, ""},
		{"nested aggregate capture", `function run(x: Box): void { let f = (): own Box[] => { let xs = [x]; return xs; }; }`, "E051"},
		{"copied f64 lanes", `function copy(x: f64x2): own f64x2 { return x; }`, ""},
		{"copied f32 lanes", `function copy(x: f32x4): own f32x4 { return x; }`, ""},
		{"copied map root", `function copy(m: Map[string, boolean]): own Map[string, boolean] { return m; }`, ""},
		{"copied map update", `function update(own m: Map[string, boolean]): own Map[string, boolean] { return m.insert("k", true); }`, ""},
		{"copied nested map", `struct Data { entries: Map[string, boolean] } function copy(d: Data): own Data { return d; }`, ""},
		{"borrowed map resource", `function bad(m: Map[string, Box]): own Map[string, Box] { return m; }`, "E051"},
		{"borrowed map cell", `function bad(m: Map[string, Cell[i32]]): own Map[string, Cell[i32]] { return m; }`, "E051"},
		{"borrowed map callback", `function bad(m: Map[string, () => i32]): own Map[string, () => i32] { return m; }`, "E051"},
		{"copied data root", `struct Data { bytes: u8[] } function copy(data: Data): own Data { return data; }`, ""},
		{"borrowed mutable root", `function bad(data: Cell[i32]): own Cell[i32] { return data; }`, "E051"},
		{"borrowed receiver", `function (x: Box) bad(): own Box[] { return [x]; }`, "E051"},
		{"aggregate loan", `function pass(x: Box): own Box[] { return [x]; }`, "E051"},
		{"aggregate local loan", `function pass(x: Box): own Box[] { let xs = [x]; return xs; }`, "E051"},
		{"aggregate helper loan", `function wrap(x: Box): Box[] { return [x]; } function pass(x: Box): own Box[] { return wrap(x); }`, "E051"},
		{"borrowed input", `function pass[T](x: T): own T { return x; }`, "E051"},
		{"borrowed lambda input", `function run(): void { let f = (x: Box): own Box => x; }`, "E051"},
		{"borrowed capture", `function run(x: Box): void { let f = (): own Box => x; }`, "E051"},
		{"owned capture", `function run(own x: Box): void { let f = (): own Box => x; }`, "E051"},
		{"strengthen callback", `function pass(own x: Box): Box { return x; } function run(): void { let f: (own Box) => own Box = pass; }`, "E003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + tc.body + ` function main(): i32 { return 0; }`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.code == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.code != "" && d.code == tc.code {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.code, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(source)
			check("Fern", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func TestSelfHostCheckedOwnedResultsNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_results_checker")
	checkedOwnedResultCases(t, bin, runner, dir)
}

func TestSelfHostCheckedOwnedResultsWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_results_checker.wasm", e2eharness.TargetWasm32Wasi)
	checkedOwnedResultCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostCheckedOwnedResultsRun(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := e2eharness.RepoPath("conformance", "cases", "checked_owned_results", "main.fern")
	want, err := os.ReadFile(filepath.Join(filepath.Dir(path), "expected.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			var cmd *exec.Cmd
			if target == e2eharness.TargetWasm32Wasi {
				bin := cli.wasmComponent(t, path, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", bin)
			} else if target == e2eharness.TargetArm64Linux {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				cmd = runArm64Bin(qemu, bin)
			} else {
				bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				cmd = runX86_64Bin(cli.runner, bin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil || !bytes.Equal(out, want) {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
			}
			var allocs, frees, live int64
			summary := leakSummaryLine(stderr.String())
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("invalid allocation census: %q (%v)", summary, err)
			}
		})
	}
}

package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The self-host's pre-codegen gate typed a method call from the METHOD NAME
// alone, so `write`, `read_line`, `read_chunk` and `first` were reserved words
// in practice: a user struct declaring one got the built-in resource signature
// (or the first same-named method declared anywhere in the module), and the
// resulting type mismatch rejected a valid program before codegen (#8462).
//
//	struct Sink { n: i32 }
//	function (k: Sink) write(s: string): i32 { … }
//	var x: i32 = k.write("abc");
//	  → error[E003]: … declared i32 but initializer has type Option[unknown]
//
// The gate now resolves on (receiver type, method name). These cases pin that:
// each defines user methods on the colliding names and asserts the values the
// user's own methods return, against the native interpreter as the oracle.
var methodRecvTypeCases = []struct {
	name string
	src  string
}{
	// The resource-method names. All three were answered from a fixed I/O
	// signature whatever the receiver was.
	{"resource_names_on_user_struct", `struct Sink { n: i32 }
function (k: Sink) write(s: string): i32 { return s.len() + k.n; }
function (k: Sink) read_line(): string { return "line"; }
function (k: Sink) read_chunk(n: i32): string { return "chunk"; }
function main(): i32 {
    var k: Sink = Sink { n: 1 };
    var x: i32 = k.write("abc");
    var y: string = k.read_line();
    var z: string = k.read_chunk(4);
    return x * 10 + y.len() + z.len();
}`},
	// A stdlib method winning the name: std/array's `(xs: T[]) first(): Option[T]`
	// claimed `Bag.first` and the call typed as Option[i32].
	{"stdlib_first_shadows_user_method", `import "std/array";
struct Bag { items: i32[] }
function (b: Bag) first(): i32[] { return b.items; }
function main(): i32 {
    var bg: Bag = Bag { items: [3, 5] };
    var f: i32[] = bg.first();
    return f[0] + f[1];
}`},
	// Two user structs declaring the same method name: the FIRST declaration won
	// for both receivers. Both must resolve to their own.
	{"same_method_name_two_structs", `struct Label { z: i32 }
function (o: Label) first(): Option[string] { return Some("sss"); }
struct Bag { items: i32[] }
function (b: Bag) first(): i32[] { return b.items; }
function main(): i32 {
    var bg: Bag = Bag { items: [3, 5] };
    var f: i32[] = bg.first();
    var n: i32 = 0;
    match (Label { z: 0 }.first()) { Some(s) => { n = s.len(); }, None => { n = 0; } }
    return n * 10 + f[0] + f[1];
}`},
	// The match-scrutinee sibling of the same defect: the payload type of
	// `match (k.read_line())` came from the name too, so a user `read_line`
	// returning Option[i32] bound its payload as a string.
	{"match_on_user_read_line", `struct Sink { n: i32 }
function (k: Sink) read_line(): Option[i32] { return Some(k.n + 6); }
function main(): i32 {
    var k: Sink = Sink { n: 1 };
    match (k.read_line()) { Some(v) => { return v; }, None => { return 0; } }
}`},
	// The type-NAME sibling of the same defect: ty_from_ref classified any bare
	// type whose spelling merely STARTED with "Map" as a coarse Map, so a user
	// struct named MapConfig / MapCloneScan / Maple had its methods typed as map
	// ops. Reached through a struct-typed FIELD, whose type still resolves by
	// name.
	{"map_prefixed_struct_name", `struct Maple { n: i32 }
function (m: Maple) get(k: i32): i32 { return m.n + k; }
struct Holder { m: Maple }
function main(): i32 {
    var h: Holder = Holder { m: Maple { n: 5 } };
    var x: i32 = h.m.get(1);
    return x;
}`},
	// Control: a real Map still dispatches as one.
	{"real_map_still_a_map", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 7);
    var v: i32 = m.get_or("a", 0);
    var h: i32 = 0;
    if (m.has("a")) { h = 1; }
    return v * 10 + m.len() + h + m.keys().len();
}`},
	// Breadth: a user struct whose methods take a handful of ordinary stdlib
	// method names at once. None may be answered from a built-in signature.
	{"common_stdlib_names_on_user_struct", `struct Doc { n: i32 }
function (d: Doc) len(): i32 { return d.n; }
function (d: Doc) get(k: i32): Option[i32] { return Some(d.n + k); }
function (d: Doc) keys(): i32[] { return [d.n]; }
function (d: Doc) contains(k: i32): boolean { return k == d.n; }
function (d: Doc) split(sep: string): i32 { return d.n + sep.len(); }
function (d: Doc) trim(): i32 { return d.n - 1; }
function (d: Doc) index_of(k: i32): i32 { return k; }
function (d: Doc) is_empty(): boolean { return d.n == 0; }
function (d: Doc) last(): i32 { return d.n; }
function main(): i32 {
    var d: Doc = Doc { n: 5 };
    var a: i32 = d.len();
    var b: i32 = 0;
    match (d.get(1)) { Some(v) => { b = v; }, None => { b = 0; } }
    var c: i32[] = d.keys();
    var e: i32 = d.split("xx");
    var f: i32 = d.trim();
    var g: i32 = d.index_of(2);
    var h: i32 = d.last();
    var r: i32 = a + b + c[0] + e + f + g + h;
    if (!d.contains(5)) { return 0 - 1; }
    if (d.is_empty()) { return 0 - 2; }
    return r;
}`},
}

// selfHostProdDriver builds the PRODUCTION self-hosted compiler (fern.fern) —
// the driver that runs the pre-codegen gate over a real program against the
// real stdlib — and returns it with the stdlib root and the x86-64 runner.
// Shared with self_host_str_runtime_stdstring_parity_test.go.
func selfHostProdDriver(t *testing.T) (fernBin, stdlibRoot string, x86runner []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), root, runner
}

// writeSelfHostProgram writes a one-file program into a fresh dir and returns
// its path.
func writeSelfHostProgram(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return path
}

// selfHostInterpOracle runs a program under the native interpreter — the value
// every self-host leg is compared against.
func selfHostInterpOracle(t *testing.T, interpBin, src string) int {
	t.Helper()
	cmd := exec.Command(interpBin, "-interp", writeSelfHostProgram(t, src))
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("interpreter did not exit normally: %s", out)
	}
	// The oracle itself must accept the program, or it pins nothing.
	if bytes.Contains(out, []byte("error[")) {
		t.Fatalf("interpreter rejected the program: %s", out)
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostMethodRecvTypeX86_64(t *testing.T) {
	fernBin, stdlibRoot, runner := selfHostProdDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range methodRecvTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := selfHostInterpOracle(t, interpBin, tc.src)
			mainPath := writeSelfHostProgram(t, tc.src)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, err := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile refused the program: %v\n%s", err, out)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(binPath)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (x86-64) = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostMethodRecvTypeArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	fernBin, stdlibRoot, runner := selfHostProdDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range methodRecvTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := selfHostInterpOracle(t, interpBin, tc.src)
			mainPath := writeSelfHostProgram(t, tc.src)
			binPath := filepath.Join(filepath.Dir(mainPath), "out.bin")
			if out, err := runX86_64Bin(runner, fernBin, "-target", "arm64-linux", mainPath, stdlibRoot, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile refused the program: %v\n%s", err, out)
			}
			cmd := runArm64Bin(qemu, binPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (arm64) = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostMethodRecvTypeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping method-receiver-type wasm cases")
	}
	fernBin, stdlibRoot, runner := selfHostProdDriver(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range methodRecvTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := selfHostInterpOracle(t, interpBin, tc.src)
			mainPath := writeSelfHostProgram(t, tc.src)
			outWat := filepath.Join(filepath.Dir(mainPath), "out.wat")
			if out, err := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile refused the program: %v\n%s", err, out)
			}
			cmd := exec.Command("wasmtime", "run", outWat)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s (wasm) = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

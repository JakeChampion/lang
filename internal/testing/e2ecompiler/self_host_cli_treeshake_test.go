package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func fnLabels(asm string) []string {
	re := regexp.MustCompile(`(?m)^(__fn_[A-Za-z_0-9]+|__method_[A-Za-z_0-9]+):`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(asm, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestSelfHostCLIPrunesStdlibImportClosureX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "std/string";
function main(): i32 {
    let s: string = "hello world";
    if (s.starts_with("hello")) { return 0; }
    return 1;
}
`
	prog := filepath.Join(dir, "ts_stdlib_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_stdlib_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)
	labels := fnLabels(asm)

	for _, dead := range []string{
		"__fn_string__repeat",
		"__fn_string__contains",
		"__fn_string__index_of",
		"__fn_string__last_index_of",
		"__fn_string__ends_with",
		"__fn_string__split",
	} {
		if strings.Contains(asm, "\n"+dead+":") {
			t.Errorf("%s is emitted, but nothing in the program can reach it — the import closure was not pruned", dead)
		}
	}
	if !strings.Contains(asm, "\n__fn_string__starts_with:") {
		t.Errorf("__fn_string__starts_with is NOT emitted, but main calls it — the prune dropped a reachable function")
	}

	const ceiling = 300
	if len(labels) > ceiling {
		t.Errorf("self-host emitted %d functions for a program using one std/string method, ceiling %d — "+
			"a count this high means the transitive import closure survived to codegen", len(labels), ceiling)
	}

	bin := buildBin(t, gcc, dir, "ts_stdlib_prog", asm)
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("pruned program exited %d, want 0", code)
	}
}

func TestSelfHostTreeshakeKeepsDropFinalizerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
function main(): i32 {
    let a: W = W { n: 7 };
    return a.n - 7;
}
`
	prog := filepath.Join(dir, "ts_drop_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_drop_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	if !strings.Contains(string(asmBytes), "\n__fn_W__drop:") {
		t.Errorf("__fn_W__drop is not emitted: the prune dropped the Drop finalizer, which the drop glue names")
	}
}

func TestSelfHostTreeshakeKeepsMapKeyMethodsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Sku { code: i32 }

@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B }

struct Hand { code: i32 }
impl cmp.Eq for Hand { function eq(self: Self, o: Self): boolean { return self.code == o.code; } }
impl cmp.Hash for Hand { function hash(self: Self): i32 { return self.code; } }

enum Mode { On(i32), Off }
impl cmp.Eq for Mode { function eq(self: Self, o: Self): boolean { return true; } }
impl cmp.Hash for Mode { function hash(self: Self): i32 { return 0; } }

function main(): i32 {
    let m: Map[Sku, i32] = map_new(8);
    m = m.insert(Sku { code: 5 }, 7);
    let t: Map[Tag, i32] = map_new(8);
    t = t.insert(A(1), 2);
    let h: Map[Hand, i32] = map_new(8);
    h = h.insert(Hand { code: 3 }, 4);
    let o: Map[Mode, i32] = map_new(8);
    o = o.insert(On(1), 6);
    return m.get_or(Sku { code: 5 }, 0) - 7 + t.get_or(A(1), 0) - 2 + h.get_or(Hand { code: 3 }, 0) - 4 + o.get_or(Off, 0) - 6;
}
`
	prog := filepath.Join(dir, "ts_derive_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_derive_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)
	for _, ty := range []string{"Sku", "Tag", "Hand", "Mode"} {
		for _, m := range []string{"eq", "hash"} {
			sym := "__fn_" + ty + "__" + m
			if !strings.Contains(asm, "\n"+sym+":") {
				t.Errorf("%s is not emitted: the prune dropped a map-key method the map lowering calls", sym)
			}
		}
	}

	callRe := regexp.MustCompile(`(?m)^[ \t]+call[ \t]+(__fn_[A-Za-z_0-9]+|__method_[A-Za-z_0-9]+)\b`)
	defined := map[string]bool{}
	for _, l := range fnLabels(asm) {
		defined[l] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range callRe.FindAllStringSubmatch(asm, -1) {
		if !defined[m[1]] && !seen[m[1]] {
			seen[m[1]] = true
			missing = append(missing, m[1])
		}
	}
	if len(missing) != 0 {
		t.Errorf("the artifact calls %d function(s) it does not define: %s — the prune dropped something reachable",
			len(missing), strings.Join(missing, ", "))
	}
}

func TestSelfHostTreeshakeQualifiesArithmeticRootsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "std/num";

struct V { n: i32 }
impl num.Add for V { function add(self: Self, o: Self): Self { return V { n: self.n + o.n }; } }
impl num.Neg for V { function neg(self: Self): V { return V { n: 0 - self.n }; } }

function main(): i32 {
    let a: V = V { n: 3 };
    let b: V = V { n: 4 };
    let c: V = a + b;
    let d: V = -c;
    return d.n + 7;
}
`
	prog := filepath.Join(dir, "ts_arith_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_arith_prog.s")
	compile := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib)
	compile.Env = append(os.Environ(), "FERN_NO_SEM_REACH=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)

	for _, live := range []string{"__fn_V__add", "__fn_V__neg", "__fn_i32__add"} {
		if !strings.Contains(asm, "\n"+live+":") {
			t.Errorf("%s is NOT emitted, but the program reaches it — the qualified root dropped a live impl", live)
		}
	}
	for _, dead := range []string{
		"__fn_bigint__BigInt__add",
		"__fn_i64__add",
		"__fn_u32__add",
		"__fn_u64__add",
		"__fn_f32__add",
		"__fn_f64__add",
	} {
		if strings.Contains(asm, "\n"+dead+":") {
			t.Errorf("%s is emitted, but nothing in the program adds that type — the root was not qualified", dead)
		}
	}

	bin := buildBin(t, gcc, dir, "ts_arith_prog", asm)
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("operator-overload program exited %d, want 0", code)
	}
}

func TestSelfHostTreeshakeKeepsDisplayToStringX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `import "std/i32";
import "std/i64";

struct Q { a: i32 }
function (q: Q) to_string(): string { return "Q!"; }

function main(): i32 {
    let q: Q = Q { a: 7 };
    write(q);
    return 0;
}
`
	prog := filepath.Join(dir, "ts_display_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_display_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)

	if !strings.Contains(asm, "\n__fn_Q__to_string:") {
		t.Errorf("__fn_Q__to_string is not emitted: the prune dropped the method the display lowering calls")
	}
	for _, dead := range []string{
		"__fn_i32__to_string",
		"__fn_i64__to_string",
		"__fn_boolean__to_string",
		"__fn_bigint__BigInt__to_string",
	} {
		if strings.Contains(asm, "\n"+dead+":") {
			t.Errorf("%s is emitted, but nothing in the program displays that type — the display root was not qualified", dead)
		}
	}

	bin := buildBin(t, gcc, dir, "ts_display_prog", asm)
	run := exec.Command(bin)
	out, err := run.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := string(out); got != "Q!" {
		t.Errorf("program printed %q, want %q — the display rewrite reached the wrong method", got, "Q!")
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("display program exited %d, want 0", code)
	}
}

func TestSelfHostTreeshakeFieldReadKeepsNoMethodX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const src = `struct Bounds { probe: i32 }
struct Dialer { tries: i32 }
function (d: Dialer) probe(): i32 {
    return d.tries + 41;
}
struct Pinger { n: i32 }
@noinline
function (p: Pinger) ping(): i32 {
    return p.n * 2;
}
function main(): i32 {
    let b: Bounds = Bounds { probe: 3 };
    let p: Pinger = Pinger { n: b.probe };
    return p.ping();
}
`
	prog := filepath.Join(dir, "ts_field_read.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_field_read.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asmBytes, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	labels := strings.Join(fnLabels(string(asmBytes)), " ")
	if strings.Contains(labels, "probe") {
		t.Errorf("Dialer.probe is emitted, but the program only reads the field `probe`:\n%s", labels)
	}
	if !strings.Contains(labels, "ping") {
		t.Errorf("Pinger.ping is NOT emitted, but main calls it:\n%s", labels)
	}
	bin := buildBin(t, gcc, dir, "ts_field_read", string(asmBytes))
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 6 {
		t.Errorf("program exited %d, want 6", code)
	}
}

func TestSelfHostTreeshakeKeepsMapDropForSignatureEnumX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	const src = `import "core/map";
enum E { A, B(Map[i32, i32]) }
function main(): i32 {
    function g(e: E): i32 { return 1; }
    return 3;
}
`
	prog := filepath.Join(dir, "ts_map_enum_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	asmPath := filepath.Join(dir, "ts_map_enum_prog.s")
	if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host compile failed: %v\n%s", err, out)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	bin := buildBin(t, gcc, dir, "ts_map_enum_prog", string(asm))
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 3 {
		t.Errorf("program exited %d, want 3", code)
	}
}

func TestSelfHostTreeshakeKeepsMapRuntimeOnlyForLiveMapsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading CLI test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	cases := []struct {
		name, src string
		maps      bool
	}{
		{"cli-without-maps", `import "std/cli";
function main(): i32 { if (cli.cli_dim("a").len() == 0) { return 1; } return 0; }
`, false},
		{"unused-map-struct", `import "core/map";
struct S { m: Map[string, string] }
function main(): i32 { return 0; }
`, false},
		{"live-map-struct", `import "core/map";
struct Opts { m: Map[string, i32] }
function main(): i32 { let o: Opts = Opts { m: map_new(4) }; let p: Opts = Opts { m: o.m.insert("a", 1) }; return p.m.len() - 1; }
`, true},
		{"signature-only", `import "core/map";
struct S { m: Map[string, string] }
function main(): i32 {
    function g(s: S): i32 { return 1; }
    return 0;
}
`, true},
		{"self-only", `import "core/map";
struct S { m: Map[i32, i32] }
trait T { function get(self: Self): i32; }
impl T for S { function get(self: Self): i32 { return 1; } }
struct U { n: i32 }
impl T for U { function get(self: Self): i32 { return self.n; } }
function f(o: dyn T): i32 { return o.get(); }
function main(): i32 { return f(U { n: 0 }); }
`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog := filepath.Join(dir, "ts_map_"+c.name+".fern")
			if err := os.WriteFile(prog, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			asmPath := filepath.Join(dir, "ts_map_"+c.name+".s")
			if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, prog, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile failed: %v\n%s", err, out)
			}
			asmBytes, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatalf("read asm: %v", err)
			}
			asm := string(asmBytes)
			if got := strings.Contains(asm, "\n__fn_map_new_impl:"); got != c.maps {
				t.Errorf("map runtime emitted = %v, want %v", got, c.maps)
			}
			bin := buildBin(t, gcc, dir, "ts_map_"+c.name, asm)
			run := exec.Command(bin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("program exited %d, want 0", code)
			}
		})
	}
}

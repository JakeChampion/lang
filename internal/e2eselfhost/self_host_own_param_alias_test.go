package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A local that takes over an `own` array parameter at its last use (`let a =
// acc`, `let a = g(acc)` at an `own` position) is threaded exactly as the
// parameter is, and a reassigned `own` array parameter releases only the
// replacements its frame minted (#10357). Answers are the interpreter's.

const ownAliasAtNode = `@noinline
function at_node(n: i32, own a: string[]): string[] {
    if (n % 3 != 1) { return a.append("g" + ""); }
    return a;
}
`

const ownAliasMain = `function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd); fd = fd + 1; }
    return pending.len() % 256;
}
`

var ownAliasCases = []struct {
	name string
	src  string
}{
	{"alias_rebind", `function at_node(n: i32, own a: string[]): string[] {
    return a.append("g" + "");
}
function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    a = at_node(n, a);
    return a;
}
` + ownAliasMain},
	{"alias_rebind_chain", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    a = at_node(n, a);
    a = at_node(n + 2, a);
    a = at_node(n + 3, a);
    a = at_node(n + 5, a);
    return a;
}
` + ownAliasMain},
	{"alias_reset", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    a = [];
    if (n % 2 == 0) { a = at_node(n, a); }
    return a;
}
` + ownAliasMain},
	{"alias_of_alias", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    let b: string[] = a;
    b = at_node(n, b);
    b = at_node(n + 2, b);
    return b;
}
` + ownAliasMain},
	{"alias_self_append", `function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    a = a.append("x" + "");
    if (n % 2 == 0) { a = a.append("y" + ""); }
    return a;
}
` + ownAliasMain},
	{"alias_loop_fn_value", `function fold(xs: i32[], own acc: string[], f: (i32, own string[]) => string[]): string[] {
    let a: string[] = acc;
    for x in xs { a = f(x, a); }
    return a;
}
` + ownAliasAtNode + `function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold([fd, fd + 1, fd + 2], pending, at_node); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	{"param_rebind_chain", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    acc = at_node(n, acc);
    acc = at_node(n + 2, acc);
    acc = at_node(n + 3, acc);
    acc = at_node(n + 5, acc);
    acc = at_node(n + 6, acc);
    return acc;
}
` + ownAliasMain},
	{"param_rebind_loop", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    for i in [n, n + 2, n + 3, n + 5, n + 6] { acc = at_node(i, acc); }
    return acc;
}
` + ownAliasMain},
	{"param_then_alias", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    acc = at_node(n, acc);
    let a: string[] = acc;
    a = at_node(n + 2, a);
    a = at_node(n + 3, a);
    return a;
}
` + ownAliasMain},
	// A call result that hands the parameter back when it keeps it.
	{"call_handback", ownAliasAtNode + `function fold(own acc: string[], n: i32): string[] {
    let out: string[] = at_node(n, acc);
    out = at_node(n + 2, out);
    out = at_node(n + 3, out);
    return out;
}
` + ownAliasMain},
	{"call_handback_then_borrowed", ownAliasAtNode + `function walk(n: i32, acc: string[]): string[] {
    if (n % 4 == 0) { return acc.append("w" + ""); }
    return acc;
}
function fold(own acc: string[], n: i32): string[] {
    let out: string[] = at_node(n, acc);
    if (n % 2 == 0) { out = walk(n, out); }
    out = at_node(n + 2, out);
    return out;
}
` + ownAliasMain},
	// A local that took the parameter over, passed in a dying position to a
	// callee that consumes it.
	{"alias_into_produced_consumer", `@noinline
function take(n: i32, own a: string[]): string[] {
    if (n % 5 == 0) { return ["r" + ""]; }
    if (n % 3 == 1) { return a; }
    return a.append("g" + "");
}
function fold(own acc: string[], n: i32): string[] {
    let a: string[] = acc;
    a = take(n, a);
    return a;
}
function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	// The same through a call that hands the parameter back.
	{"call_handback_into_produced_consumer", `@noinline
function take(n: i32, own a: string[]): string[] {
    if (n % 5 == 0) { return ["r" + ""]; }
    if (n % 3 == 1) { return a; }
    return a.append("g" + "");
}
@noinline
function keep(n: i32, own a: string[]): string[] {
    if (n % 4 != 1) { return a.append("k" + ""); }
    return a;
}
function fold(own acc: string[], n: i32): string[] {
    let a: string[] = keep(n, acc);
    a = take(n, a);
    return a;
}
function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	// A call through a fn-typed parameter hands the parameter back.
	{"fn_value_param_handback", `@noinline
function at_node(n: i32, own a: string[]): string[] {
    if (n % 3 != 1) { return a.append("g" + ""); }
    return a;
}
function fold(own acc: string[], n: i32, f: (i32, own string[]) => string[]): string[] {
    let a: string[] = f(n, acc);
    a = f(n + 2, a);
    return a;
}
function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd, at_node); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	// A call through a fn-typed local: the registry reads its `own` positions as
	// the lowering stamps them, so both take `a` for `acc`.
	{"fn_value_local_into_produced_consumer", `@noinline
function at_node(n: i32, own a: string[]): string[] {
    if (n % 3 != 1) { return a.append("g" + ""); }
    return a;
}
@noinline
function take(n: i32, own a: string[]): string[] {
    if (n % 5 == 0) { return ["r" + ""]; }
    if (n % 3 == 1) { return a; }
    return a.append("g" + "");
}
function fold(own acc: string[], n: i32): string[] {
    let f: (i32, own string[]) => string[] = at_node;
    let a: string[] = f(n, acc);
    a = take(n, a);
    return a;
}
function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	// A consuming position's parameter dropped by a return (#10361).
	{"consuming_param_dropped", `@noinline
function at_node(n: i32, own a: string[]): string[] {
    if (n % 3 != 1) { return a.append("g" + ""); }
    return a;
}
function fold(own acc: string[], n: i32): string[] { acc = at_node(n, acc); acc = at_node(n + 2, acc); if (n % 2 == 0) { return []; } return acc; }
function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = fold(pending, fd); fd = fd + 1; }
    return pending.len() % 256;
}
`},
	// #9409: a borrowed alias moved into an `own` callee whose body aliases
	// the parameter in turn.
	{"borrowed_outer", `function grow(x: string, own acc: string[]): string[] { return acc.append(x); }
function inner(x: string, own acc: string[]): string[] {
    let a: string[] = acc;
    a = grow(x, a);
    return a;
}
function outer(x: string, acc: string[]): string[] {
    let a: string[] = acc;
    a = inner(x, a);
    return a;
}
function main(): i32 {
    let seed: string[] = ["seed-one", "seed-two"];
    let out: string[] = outer("alpha", seed);
    return (out.len() * 10 + seed.len()) % 251;
}
`},
	// The shape #10338's checker.inst_stmts walk takes, over a struct array.
	{"struct_elem_walk", `struct Inst { name: string, depth: i32 }
function fold(xs: i32[], own acc: Inst[], f: (i32, own Inst[]) => Inst[]): Inst[] {
    let a: Inst[] = acc;
    for x in xs { a = f(x, a); }
    return a;
}
function step(st: i32, depth: i32, own acc: Inst[]): Inst[] {
    function at_node(n: i32, own a: Inst[]): Inst[] {
        if (n % 3 == 0) { return a.append(Inst { name: "g" + "", depth: depth }); }
        return a;
    }
    return fold([st, st + 1], acc, at_node);
}
function walk(stmts: i32[], acc: Inst[]): Inst[] {
    let out: Inst[] = acc;
    for st in stmts { out = step(st, 1, out); }
    return out;
}
function main(): i32 {
    let pending: Inst[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = walk([fd, fd + 1], pending); fd = fd + 1; }
    let t: i32 = 0;
    for p in pending { t = t + p.name.len() + p.depth; }
    return t % 256;
}
`},
	{"struct_elem_scope", `import "std/i32";
struct Inst { name: string, cts: string[], depth: i32 }
struct Sc { names: string[], n: i32 }
function bind(st: i32, s: Sc): Sc {
    if (st % 2 == 0) { return s; }
    return Sc { names: s.names.append("x" + ""), n: s.n + 1 };
}
function fold(xs: i32[], own acc: Inst[], f: (i32, own Inst[]) => Inst[]): Inst[] {
    let a: Inst[] = acc;
    for x in xs { a = f(x, a); }
    return a;
}
function step(st: i32, cur: Sc, depth: i32, own acc: Inst[]): Inst[] {
    function at_node(n: i32, own a: Inst[]): Inst[] {
        if (n % 3 == 0) { return a.append(Inst { name: "g" + "", cts: [cur.n.to_string()], depth: depth }); }
        return a;
    }
    let xs: i32[] = [st, st + 1, st + 2];
    return fold(xs, acc, at_node);
}
function walk(stmts: i32[], s: Sc, depth: i32, acc: Inst[]): Inst[] {
    let out: Inst[] = acc;
    let cur: Sc = s;
    for st in stmts {
        out = step(st, cur, depth, out);
        cur = bind(st, cur);
    }
    return out;
}
function body(fd: i32, depth: i32, acc: Inst[]): Inst[] {
    if (fd == 5) { return acc; }
    let sc: Sc = Sc { names: ["p" + ""], n: fd };
    return walk([fd, fd + 1, fd + 2, fd + 3], sc, depth, acc);
}
function main(): i32 {
    let pending: Inst[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = body(fd, 0, pending); fd = fd + 1; }
    let wi: i32 = 0;
    let t: i32 = 0;
    while (wi < pending.len()) {
        let inst: Inst = pending[wi];
        wi = wi + 1;
        t = t + inst.cts.len() + inst.name.len();
        if (wi < 5) { pending = body(wi + 20, inst.depth + 1, pending); }
    }
    return t % 256;
}
`},
}

func ownAliasOracle(t *testing.T, interp, src string) int {
	t.Helper()
	cmd := exec.Command(interp, "-interp", src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("interpreter did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func writeOwnAliasSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostOwnParamAliasX86_64(t *testing.T) {
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range ownAliasCases {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, want, stderr)
			}
		})
	}
}

func TestSelfHostOwnParamAliasArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range ownAliasCases {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), tc.name, string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Fatalf("exit = %d, want %d\n%s", code, want, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostOwnParamAliasWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	interp := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range ownAliasCases {
		src := writeOwnAliasSrc(t, tc.name, tc.src)
		want := ownAliasOracle(t, interp, src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != want {
				t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

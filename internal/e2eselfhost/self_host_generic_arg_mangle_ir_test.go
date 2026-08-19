package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostGenericArgMangleIR pins a loader defect: a module-local type used
// as a GENERIC ARGUMENT was never mangled.
//
// flatten's rewrite_type_name splits a bracketed spelling into base + suffix and
// re-attaches the suffix verbatim, so `find(): Option[Flag]` kept `Option[Flag]`
// while the struct declaration became `lib__Flag`. Nothing then resolves the
// payload type of the match binder, so `f.<field>` on it cannot lower and the
// whole module bails.
//
// This is the SAME defect the tuple case was fixed for — `(string, Box)` keeping
// an unmangled `Box`, which dispatched a non-existent `Box.method` — so it is a
// loader correctness bug in its own right, not only an IR-routing one. The
// generic case simply had no test.
//
// It was the last construct keeping the self-host drivers on the legacy AST
// emitters (#3457 slice 5): std/cli's `parse` reaches it through
// `match (__cli_find_short(...)) { Some(f) => f.takes_value }`, which is what
// TestSelfHostStdTestE2E/cli compiles.
//
// The program covers the shapes that share the code path: the Option payload
// (the reported case), a Result Ok payload, a nested `Option[Flag][]` (array
// depth rides alongside the arguments), and a `Map`-style two-argument generic
// where only the second argument is module-local. Every result is CONSUMED at
// its own type, so a half-recovered spelling shows up as a wrong exit code
// rather than as a bail.
func TestSelfHostGenericArgMangleIR(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	files := map[string]string{
		"lib.fern": `pub struct Flag { long: string, takes_value: boolean }

pub function find(fs: Flag[], n: string): Option[Flag] {
    var i: i32 = 0;
    while (i < fs.len()) {
        if (fs[i].long == n) { return Some(fs[i]); }
        i = i + 1;
    }
    return None;
}

// The reported shape: an inline match whose binder is a module-local struct.
pub function probe(fs: Flag[], n: string): i32 {
    match (find(fs, n)) {
        Some(f) => { if (f.takes_value) { return f.long.len(); } return 1; },
        None => { return 0; }
    }
}

// The Result Ok arm goes through the same argument list.
pub function checked(fs: Flag[], n: string): Result[Flag, string] {
    match (find(fs, n)) {
        Some(f) => { return Ok(f); },
        None => { return Err("missing"); }
    }
}

pub function checked_len(fs: Flag[], n: string): i32 {
    match (checked(fs, n)) {
        Ok(f) => { return f.long.len(); },
        Err(e) => { return e.len(); }
    }
}

// An ARRAY of the generic: array depth is re-attached after the arguments, so a
// rewrite that drops it would mis-spell this one specifically.
pub function firsts(fs: Flag[]): Option[Flag][] {
    var out: Option[Flag][] = [];
    var i: i32 = 0;
    while (i < fs.len()) { out = out.append(Some(fs[i])); i = i + 1; }
    return out;
}

pub function firsts_len(fs: Flag[]): i32 {
    var os: Option[Flag][] = firsts(fs);
    match (os[0]) {
        Some(f) => { return f.long.len(); },
        None => { return 0; }
    }
}
`,
		"main.fern": `import "./lib";

function main(): i32 {
    var fs: lib.Flag[] = [lib.Flag { long: "name", takes_value: true }, lib.Flag { long: "v", takes_value: false }];
    var t: i32 = lib.probe(fs, "name");        // 4  (takes_value -> "name".len())
    t = t + lib.probe(fs, "v");                // +1 (!takes_value -> 1)
    t = t + lib.probe(fs, "zz");               // +0 (None)
    t = t + lib.checked_len(fs, "name");       // +4 (Ok payload)
    t = t + lib.checked_len(fs, "zz");         // +7 ("missing".len())
    t = t + lib.firsts_len(fs);                // +4 (Option[Flag][] element)
    return t;                                  // 20
}
`,
	}
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asm := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern")))
	if !strings.Contains(asm, ".Lir") {
		t.Fatal("program did not route through the IR path — the generic argument is unmangled again")
	}
	bin := buildBin(t, gcc, progDir, "generic_arg_mangle", asm)
	if _, exit := runBin(binCmd(runner, bin), ""); exit != 20 {
		t.Errorf("exit = %d, want 20", exit)
	}
}

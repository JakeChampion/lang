package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Qualified imported-union-member match patterns (`mod.Variant(x)`) on the
// self-host IR path. A `type Line = Row | Blank` union whose members are
// imported structs is matched via a QUALIFIED pattern `rows.Row(r)`. The
// pattern's type_name stays qualified ("rows.Row") through flatten (rewrite_pattern
// does not mangle it, unlike ctor / field references), while the member struct's
// flattened decl name is the MANGLED "rows__Row". The match-lowering used to strip
// the qualifier to the bare "Row", which matches only a same-module ENUM variant —
// for an imported union member it missed decl_is_struct and BAILED the whole
// function (and on the AST emitter it fell to, the untyped payload's
// `r.cells.len()` on a `string[]` field mis-dispatched to an undefined
// `i32__len`). The lowering now tries
// the '.'->'__' mangled form first, so the member lowers through the IR path AND
// binds `r` typed "rows__Row" — so `r.cells.len()` dispatches as a string[] read.
// (This widening flips the self-host's own `parser.*` matches to IR too; the extra
// whole-compiler codegen is kept under the per-module-emit ceiling by the #3425
// function-window sharding.)
//
// Asserts: the module decides `ir` (the qualified pattern no longer forces an AST
// bail), the emitted asm has no `__fn_i32__len`, and the self-host binary's exit
// code matches the native interpreter oracle.
func TestSelfHostUnionVariantFieldIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// The imported library: a union of two structs, one carrying a string[]
	// field (the shape that exposed the mis-dispatch).
	lib := `pub struct Row { cells: string[] }
pub struct Blank {  }
pub type Line = Row | Blank;

pub function mk_row(cells: string[]): Line { return Row { cells: cells }; }
pub function mk_blank(): Line { return Blank {  }; }
`
	if err := os.WriteFile(filepath.Join(dir, "uvrows.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}

	// count() matches the qualified imported-union members and reads the string[]
	// field's length off the bound payload — the case that used to bail. The
	// zero-field variant is matched with `_` (a binding on a zero-field struct
	// trips the module's field-count bail and would refuse it for an
	// unrelated reason, masking the qualified-pattern behaviour under test).
	entrySrc := `import "./uvrows";

function count(l: uvrows.Line): i32 {
    match (l) {
        uvrows.Row(r) => { return r.cells.len(); },
        uvrows.Blank(_) => { return 0; },
    }
    return -1;
}

function main(): i32 {
    var a: uvrows.Line = uvrows.mk_row(["x", "y", "z"]);
    var b: uvrows.Line = uvrows.mk_blank();
    return count(a) * 10 + count(b);
}
`
	entry := filepath.Join(dir, "uvmain.fern")
	if err := os.WriteFile(entry, []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	_, want := runFixtureInterp(t, entry, "")
	if want != 30 {
		t.Fatalf("native oracle = %d, want 30 (sanity: count(a)=3 cells, count(b)=0)", want)
	}

	if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
		t.Errorf("decide = %q, want \"ir\" (qualified imported-union match still bails)", strings.TrimSpace(out))
	}

	asm, _ := runDriver(entry, root)
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes")
	}
	if strings.Contains(asm, "__fn_i32__len") {
		t.Error("emitted asm references undefined __fn_i32__len (payload string[] field mis-dispatched as i32)")
	}
	bin := buildBin(t, gcc, dir, "uvmain_bin", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("self-host run = %d, want %d (native oracle)", code, want)
	}
}

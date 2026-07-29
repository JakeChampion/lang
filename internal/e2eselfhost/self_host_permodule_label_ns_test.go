package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Unit-local `.L<n>` labels must be namespaced per module, like the `.S<n>`
// string-literal labels already are.
//
// asm_modload_run's over-budget rescue (emit_per_module_concat: a merged module
// between 512 and 1500 functions emits per-module so each unit fits the IR
// budget, rather than dropping the whole program to the AST emitter) writes
// every unit into ONE assembly file. Each unit starts a fresh EmitState, so
// asmcore.fresh_label's counter restarts at 0 per unit — and two units that both
// mint a label collide. GAS's `.L` prefix keeps a symbol out of the symtab; it
// does not make two definitions in one file legal, so the assembler rejects the
// program outright:
//
//	fp3.s:7869: Error: symbol `.L0' is already defined
//
// An f64 literal is the ordinary way to reach fresh_label on the IR path (the
// literal's IEEE-754 bits go to .rodata under a fresh label), so the shape that
// broke is unremarkable: import a stdlib module that contains float literals,
// use a float literal in main. `import "std/float"` plus any f-string of a
// float is exactly that, and it pulls in ~825 functions — comfortably inside
// the per-module rescue's window.
//
// str_ns already existed for this purpose; fresh_label just wasn't reading it.
// With the fix a unit's labels read `.Lfloat_0` / `.L__entry_0`, matching the
// `.Sfloat_0` string pool beside them.

// floatLabelNSProg formats floats through std/float, and holds float literals in
// main as well: the collision needs a fresh_label user in TWO units, so a
// program that only formats (literals confined to the library) would not
// reproduce it.
const floatLabelNSProg = `import "std/float";
function main(): i32 {
    var a: f64 = 1.0 / 3.0;
    var b: f64 = 0.1;
    write(a.to_string() + "|" + b.to_string() + "|" + a.to_string_prec(4) + "\n");
    return 0;
}
`

// labelDef matches an assembly label DEFINITION at the start of a line — both
// the `.L…`/`.S…` unit-local pools and the `.Lir_…` control-flow labels, since a
// duplicate of any of them is equally fatal.
var labelDef = regexp.MustCompile(`(?m)^(\.[A-Za-z_][A-Za-z0-9_$]*):`)

// TestSelfHostPerModuleLabelNS_X86_64 pins that the per-module concat path emits
// no duplicate labels and that its output is correct, using the real stdlib.
func TestSelfHostPerModuleLabelNS_X86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	interpBin := buildLangBinForInterp(t)

	// The driver resolves `std/…` relative to the ENTRY's directory, so the
	// program is written at the root of a copy of internal/stdlib.
	progDir := t.TempDir()
	copyStdlibTree(t, "../../internal/stdlib", progDir)
	entry := filepath.Join(progDir, "main.fern")
	if err := os.WriteFile(entry, []byte(floatLabelNSProg), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	asm := string(runDriverFile(t, runner, driverBin, entry))
	if len(asm) == 0 {
		t.Fatal("driver emitted 0 bytes")
	}

	// The case only guards the bug while it takes the per-module path. That
	// path is the only producer of namespaced labels, so their presence is the
	// route assertion.
	if !strings.Contains(asm, ".L__entry_") {
		t.Fatal("no namespaced labels: the program no longer routes per-module (import set changed?) — pick one that does")
	}

	var dups []string
	seen := map[string]bool{}
	for _, m := range labelDef.FindAllStringSubmatch(asm, -1) {
		if seen[m[1]] {
			dups = append(dups, m[1])
			continue
		}
		seen[m[1]] = true
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		if len(dups) > 8 {
			dups = dups[:8]
		}
		t.Errorf("duplicate label definitions across per-module units: %v", dups)
	}

	// The assembler is the real gate — a duplicate is an error there, not a
	// warning — and running it against the interpreter proves the namespacing
	// did not misdirect any reference.
	want, err := exec.Command(interpBin, "-interp", entry).Output()
	if err != nil {
		t.Fatalf("interp oracle: %v", err)
	}
	progBin := buildBin(t, gcc, progDir, "labelns", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	got, rerr := cmd.Output()
	if rerr != nil {
		t.Fatalf("run: %v", rerr)
	}
	if string(got) != string(want) {
		t.Errorf("stdout = %q, want %q (interp oracle)", got, want)
	}
}

// copyStdlibTree copies the stdlib source tree so a program can sit at its root
// and resolve `std/…` / `core/…` imports the way the driver expects.
func copyStdlibTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !strings.HasSuffix(path, ".fern") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy stdlib tree: %v", err)
	}
}

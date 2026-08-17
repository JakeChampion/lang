package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Two enums in one program may declare the SAME variant name. The parser
// desugars every variant into a StructDecl keyed by the bare name, so with
// `enum A { None, … }` and `enum B { None, … }` there are two decls called
// `None` — and the by-name lookups (`variant_enum_owner`, `decl_field_count`,
// `struct_field_is_i64`) all answer for whichever was declared FIRST.
//
// The two qualified-EXPRESSION sites asked the wrong question of that table:
// they took the first decl's owner and tested it for equality with the
// qualifier, so `B.None` saw owner `A` and fell through to a struct-field
// read, bailing the module. `variant_decl_index` asks whether a decl with
// that name owned by THAT enum exists, which is the question a qualified
// reference actually poses.
//
// Not a miscompile in any shape measured — the by-name lookups are uniformly
// first-wins, so the one enum that did lower stayed self-consistent and the
// other refused outright. With the AST emitter retired that refusal is a hard
// compile error, so these are programs native builds and the self-host would
// not.
//
// The qualified-PATTERN side already worked: it is scrutinee-driven, so it
// never consulted the flat table this way.
var sharedVariantNameCases = []struct {
	name string
	src  string
}{
	// The reported shape: a payload-less variant name shared by two enums.
	{"shared-unit-variant", `enum A1 { None, Xx(i32) }
enum B1 { None, Yy(i32) }
function main(): i32 { var a: A1 = A1.None; var b: B1 = B1.None; var r: i32 = 0;
    match (a) { A1.None => { r = r + 1; }, _ => {} }
    match (b) { B1.None => { r = r + 4; }, _ => {} }
    return r; }`},
	// Nothing about it is builtin-specific — a name with no builtin
	// counterpart collides identically.
	{"shared-unit-variant-non-builtin", `enum A3 { Zed, Xx(i32) }
enum B3 { Zed, Yy(i32) }
function main(): i32 { var a: A3 = A3.Zed; var b: B3 = B3.Zed; var r: i32 = 0;
    match (a) { A3.Zed => { r = r + 1; }, _ => {} }
    match (b) { B3.Zed => { r = r + 4; }, _ => {} }
    return r; }`},
	// The payload-carrying construction site, which bailed with
	// `function value B2 not defined` rather than a field-access tag.
	{"shared-payload-variant", `enum A2 { Wrap(i32), P }
enum B2 { Wrap(i32), Q }
function main(): i32 { var a: A2 = A2.Wrap(1); var b: B2 = B2.Wrap(4); var r: i32 = 0;
    match (a) { A2.Wrap(n) => { r = r + n; }, _ => {} }
    match (b) { B2.Wrap(n) => { r = r + n; }, _ => {} }
    return r; }`},
	// Only the SECOND-declared enum used: the first-match lookup made this
	// the failing half, so exercising it alone is the sharper case.
	{"second-enum-only", `enum A9 { None, Xx(i32) }
enum B9 { None, Yy(i32) }
function main(): i32 { var b: B9 = B9.None; match (b) { B9.None => { return 4; }, _ => { return 1; } } }`},
	// Shared name at different ordinals, so nothing can be riding on the
	// two variants happening to sit at the same index.
	{"shared-name-different-ordinals", `enum A5 { None, Xx(i32) }
enum B5 { Yy(i32), None }
function main(): i32 { var a: A5 = A5.None; var b: B5 = B5.None; var r: i32 = 0;
    match (a) { A5.None => { r = r + 1; }, _ => {} }
    match (b) { B5.None => { r = r + 4; }, _ => {} }
    return r; }`},
	// A single enum, unchanged by the fix — the by-name and by-owner
	// lookups agree whenever the name is unique.
	{"single-enum-control", `enum S1 { None, Xx(i32) }
function main(): i32 { var a: S1 = S1.None; match (a) { S1.None => { return 1; }, _ => { return 9; } } }`},
}

func TestSelfHostSharedVariantNameIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range sharedVariantNameCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, prog)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

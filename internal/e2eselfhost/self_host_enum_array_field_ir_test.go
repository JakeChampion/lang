package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enumArrayFieldIRCases exercise ENUM-ARRAY (`E[]`) struct-literal field VALUES
// through the self-host IR path: the three construction shapes the irlower
// struct-lit gate now admits for an array-of-enum field, alongside the
// array-of-struct forms it already accepted —
//   - a bare-ident enum-array local       (`S { items: one }`)
//   - a `.append` on a borrowed param      (`S { items: items.append(B(v)) }`)
//   - a field-access copy                   (`S { items: a.items }`)
//
// Enum-array struct fields take the IDENTICAL is_unique-gated deep-drop as
// struct-array fields (emit_struct_field_drops' k_box walk), so the same alias-
// inc / no-inc decisions are sound; this is the construction-side widening that
// matches the already-shipped drop side. It is the prerequisite for routing
// parser.dl_collect_stmts (a `DeferAcc { stmts, actions, flags }` builder over
// `Stmt[]`) through the IR — the goal-1 frontier toward retiring the AST
// emitters.
//
// Each program builds an enum array into a struct field and sums it back, so a
// botched alias-inc (over-release → wrong/garbage element) or a missing field
// (bail → AST) is caught by the exit code; the asm-size bound pins the IR path
// (the AST map/heap runtime is ~40 KB, the IR runtime ~10-12 KB).
var enumArrayFieldIRCases = []struct {
	name string
	src  string
	want int
}{
	// Array-literal field value (already broadly supported; the baseline).
	{"literal", `enum N { A(i32), B(i32) }
struct S { items: N[], k: i32 }
function sum_n(xs: N[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { match (xs[i]) { A(x) => { s = s + x; }, B(x) => { s = s + x; } } i = i + 1; } return s; }
function main(): i32 { var s: S = S { items: [A(3), B(4)], k: 2 }; return sum_n(s.items) + s.k; }`, 9},

	// Bare-ident enum-array local as the field value (`S { items: one }`).
	{"bare-ident", `enum N { A(i32), B(i32) }
struct S { items: N[], k: i32 }
function sum_n(xs: N[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { match (xs[i]) { A(x) => { s = s + x; }, B(x) => { s = s + x; } } i = i + 1; } return s; }
function main(): i32 { var one: N[] = [A(5), B(1)]; var s: S = S { items: one, k: 1 }; return sum_n(s.items) + s.k; }`, 7},

	// `.append` on a borrowed param as the field value (`S { items: items.append(B(v)) }`).
	{"append-param", `enum N { A(i32), B(i32) }
struct S { items: N[], k: i32 }
function sum_n(xs: N[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { match (xs[i]) { A(x) => { s = s + x; }, B(x) => { s = s + x; } } i = i + 1; } return s; }
function build(items: N[], v: i32): S { return S { items: items.append(B(v)), k: items.len() }; }
function main(): i32 { var st: N[] = [A(2)]; var s: S = build(st, 5); return sum_n(s.items) + s.k; }`, 8},

	// Field-access copy as the field value (`S { items: a.items }`) — the er.actions shape.
	{"field-copy", `enum N { A(i32), B(i32) }
struct S { items: N[], k: i32 }
function sum_n(xs: N[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { match (xs[i]) { A(x) => { s = s + x; }, B(x) => { s = s + x; } } i = i + 1; } return s; }
function cp(a: S): S { return S { items: a.items, k: a.k }; }
function main(): i32 { var s0: S = S { items: [A(6), B(0)], k: 3 }; var s1: S = cp(s0); return sum_n(s1.items) + s1.k; }`, 9},
}

// TestSelfHostEnumArrayFieldIRX86_64 routes each case through the x86-64 IR
// driver (asm_run → asm.emit_module → emit_module_ir when all_eligible).
func TestSelfHostEnumArrayFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range enumArrayFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 28000 {
				t.Fatalf("%s: asm is %d bytes — expected the compact IR output, not the AST runtime (a bail)", tc.name, len(asm))
			}
			progBin := buildBin(t, gcc, dir, "eaf_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostEnumArrayFieldIRArm64 runs the same cases through the arm64 IR
// backend (asm_ir_run -target arm64 → asm_arm64.emit_module's use_ir branch →
// asm_arm64_ir.emit_body, sharing irlower's enum-array-field lowering). This is
// the load-bearing arm64 check: an enum-array struct field's deep-drop rides
// arm64's heap-element reclamation, so an over-release here surfaces as a wrong
// exit code / crash under qemu. Routing through the production emit (no -ir flag,
// so asm_arm64.emit_module's own use_ir dispatch runs) is deliberate — only it
// injects the builtin enums (module_with_builtins) that enum-eligibility needs;
// the differential -ir driver bails every enum program to AST. IR routing is
// pinned by the arm64 IR emitter's `.Lira_` label marker rather than a size
// bound (arm64's IR runtime is ~48-55 KB, close to the AST runtime size).
func TestSelfHostEnumArrayFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm.fern", "asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range enumArrayFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-target", "arm64")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed (%d bytes, err %v)", tc.name, len(asm), err)
			}
			// `.Lira_` is the arm64 IR emitter's per-function label prefix
			// (asm_arm64_ir); its presence proves the module routed through the
			// IR path, not the AST fallback.
			if !strings.Contains(string(asm), ".Lira_") {
				t.Fatalf("%s: arm64 asm has no .Lira_ marker — module bailed to the AST path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "eaf_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

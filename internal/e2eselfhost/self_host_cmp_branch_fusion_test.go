package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Compare-and-branch fusion in the self-host IR backends (#8425), the mirror of
// what internal/codegen has carried since #4378 / #8194: an integer comparison
// whose 0/1 result flows straight into the `if` / `br_if` that follows it
// (through zero or more `not`s) emits `cmp` plus one conditional jump rather
// than materialising the boolean and testing it again.
//
// Two things are pinned per backend and they have to travel together. The SHAPE
// says the fusion fired — the fused pair appears and the setcc/cset chain is
// gone from the function; on its own an emitter that dropped the branch outright
// would satisfy it. The MATRIX says the polarity is right: the jump mnemonic is
// picked from the comparison, its signedness and the parity of the `not` run, so
// a slip in any of the three inverts one branch, and that is a silent miscompile
// no shape assertion can see. Every combination therefore runs, against answers
// computed here rather than by the compiler under test, and scored arithmetically
// rather than by branching on them — see cmpBranchMatrix for why that matters.

var cmpOps = []string{"<", "<=", ">", ">=", "==", "!="}

// cmpEval is the oracle for one comparison, evaluated on the raw bit patterns
// so the unsigned matrix can carry values with bit 63 set — the ones where the
// below/above jump family and the less/greater one disagree.
func cmpEval(op string, a, b uint64, unsigned bool) bool {
	if unsigned {
		switch op {
		case "<":
			return a < b
		case "<=":
			return a <= b
		case ">":
			return a > b
		case ">=":
			return a >= b
		case "==":
			return a == b
		}
		return a != b
	}
	x, y := int64(a), int64(b)
	switch op {
	case "<":
		return x < y
	case "<=":
		return x <= y
	case ">":
		return x > y
	case ">=":
		return x >= y
	case "==":
		return x == y
	}
	return x != y
}

func cmpLit(v uint64, unsigned bool) string {
	if unsigned {
		return strconv.FormatUint(v, 10)
	}
	return strconv.FormatInt(int64(v), 10)
}

// cmpBranchMatrix builds a program that exits with the NUMBER of combinations
// disagreeing with the answer written beside them — 0 when every comparison x
// negation depth x consumer combination is right. The three consumers are the
// three shapes the fusion sees: an `if` with no else (its jump targets the end),
// an `if/else` (it targets a real else-label), and a `while` guard (a `br_if`,
// which the lowering reaches through a `not` the source never wrote).
//
// main accumulates `(got - want)^2` rather than branching on each answer, and
// that is load-bearing rather than a style: a check written as `if (got != want)
// { return … }` is measured by the very lowering under test, so a polarity slip
// that inverts EVERY branch inverts the checks too and the program reports
// itself clean. The arithmetic form has no branch to invert.
func cmpBranchMatrix(ty string, unsigned bool, pairs [][2]uint64) string {
	var b strings.Builder
	var checks []string
	id := 0
	b.WriteString("function sq(x: i32): i32 { return x * x; }\n")
	for _, op := range cmpOps {
		for _, nots := range []int{0, 1, 2} {
			cond := strings.Repeat("!", nots) + "(a " + op + " b)"
			fmt.Fprintf(&b, "function fi%d(a: %s, b: %s): i32 { if (%s) { return 1; } return 0; }\n", id, ty, ty, cond)
			fmt.Fprintf(&b, "function fe%d(a: %s, b: %s): i32 { if (%s) { return 1; } else { return 0; } }\n", id, ty, ty, cond)
			// The body returns on its first pass, so the loop runs at most once
			// however the guard resolves — the answer is the guard's initial value.
			fmt.Fprintf(&b, "function fw%d(a: %s, b: %s): i32 { var n: i32 = 0; while (%s) { n = n + 1; if (n > 0) { return n; } } return 0; }\n", id, ty, ty, cond)
			for _, p := range pairs {
				want := 0
				if cmpEval(op, p[0], p[1], unsigned) != (nots%2 == 1) {
					want = 1
				}
				for _, fn := range []string{"fi", "fe", "fw"} {
					checks = append(checks, fmt.Sprintf("%s%d(%s, %s) - %d",
						fn, id, cmpLit(p[0], unsigned), cmpLit(p[1], unsigned), want))
				}
			}
			id++
		}
	}
	b.WriteString("function main(): i32 {\n  var bad: i32 = 0;\n")
	for _, c := range checks {
		fmt.Fprintf(&b, "  bad = bad + sq(%s);\n", c)
	}
	b.WriteString("  return bad;\n}\n")
	return b.String()
}

// signedPairs and unsignedPairs cover the orderings in both directions plus
// equality. The unsigned set's last pair has bit 63 set, so a signed jump
// mnemonic chosen for it answers the opposite of the truth.
var signedPairs = [][2]uint64{
	{uint64(1), uint64(2)},
	{uint64(2), uint64(2)},
	{uint64(5), uint64(2)},
	{uint64(0xFFFFFFFFFFFFFFFD), uint64(2)}, // -3
}

var unsignedPairs = [][2]uint64{
	{1, 2},
	{2, 2},
	{5, 2},
	{18000000000000000000, 2},
}

// fusedFnBody returns the emitted lines of `__fn_<name>`, for shape assertions.
func fusedFnBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n__fn_"+name+":\n")
	if start < 0 {
		t.Fatalf("function %q not found in emitted asm", name)
	}
	rest := asm[start+1:]
	if end := strings.Index(rest, ".cfi_endproc"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestSelfHostCmpBranchFusion(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, target, src string) string {
		t.Helper()
		cmd := runX86_64Bin(x86runner, driverBin, "-target", target)
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %s: %v", target, err)
		}
		return string(out)
	}

	// Shape: one source per backend-visible decision the fusion makes — the
	// signed mnemonic, the unsigned one, and a `not` run with no comparison in
	// front of it (the boolean the branch tests for zero itself).
	shape := []struct {
		name string
		src  string
		// want are substrings the fused function must contain; dead are the
		// materialisation's instructions, which must be gone from it.
		wantX86, deadX86 []string
		wantArm, deadArm []string
	}{
		{
			name:    "signed",
			src:     "function f(a: i32, b: i32): i32 { if (a < b) { return 1; } return 2; }\nfunction main(): i32 { return f(1, 2); }",
			wantX86: []string{"cmpq %rcx, %rax\n    jge "},
			deadX86: []string{"setl", "movzbq %al"},
			wantArm: []string{"cmp x0, x1\n    b.ge "},
			deadArm: []string{"cset"},
		},
		{
			// u64 `<` must take the below/above family: the two disagree across
			// the sign boundary, and a wrong pick silently miscompiles.
			name:    "unsigned",
			src:     "function f(a: u64, b: u64): i32 { if (a < b) { return 1; } return 2; }\nfunction main(): i32 { return f(1, 2); }",
			wantX86: []string{"cmpq %rcx, %rax\n    jae "},
			deadX86: []string{"setb", "movzbq %al"},
			wantArm: []string{"cmp x0, x1\n    b.hs "},
			deadArm: []string{"cset"},
		},
		{
			// A boolean that is not a comparison's result: the `not` emits
			// nothing and the branch inverts its own zero test.
			name:    "not-only",
			src:     "function f(a: boolean): i32 { if (!a) { return 1; } return 2; }\nfunction main(): i32 { return f(false); }",
			wantX86: []string{"testq %rax, %rax\n    jnz "},
			deadX86: []string{"setz", "movzbq %al"},
			wantArm: []string{"cbnz x0, "},
			deadArm: []string{"cset x0, eq"},
		},
		{
			// The loop guard from #8425's reproduction: `while (i < n)` reaches
			// its `br_if` through the lowering's own `not`, so the fusion has to
			// see through it to the comparison.
			name:    "loop-guard",
			src:     "function f(n: i32): i32 { var i: i32 = 0; var c: i32 = 0; while (i < n) { c = c + 2; i = i + 1; } return c; }\nfunction main(): i32 { return f(3); }",
			wantX86: []string{"cmpq %rcx, %rax\n    jge "},
			deadX86: []string{"setl", "setz"},
			wantArm: []string{"cmp x0, x1\n    b.ge "},
			deadArm: []string{"cset"},
		},
	}

	t.Run("x86-64", func(t *testing.T) {
		for _, sc := range shape {
			t.Run("shape-"+sc.name, func(t *testing.T) {
				body := fusedFnBody(t, emit(t, "x86-64-linux", sc.src), "f")
				for _, w := range sc.wantX86 {
					if !strings.Contains(body, w) {
						t.Errorf("missing fused %q in:\n%s", w, body)
					}
				}
				for _, d := range sc.deadX86 {
					if strings.Contains(body, d) {
						t.Errorf("un-fused %q still emitted in:\n%s", d, body)
					}
				}
			})
		}
		for _, m := range []struct {
			name     string
			ty       string
			unsigned bool
			pairs    [][2]uint64
		}{
			{"matrix-signed", "i32", false, signedPairs},
			{"matrix-unsigned", "u64", true, unsignedPairs},
		} {
			t.Run(m.name, func(t *testing.T) {
				src := cmpBranchMatrix(m.ty, m.unsigned, m.pairs)
				asm := emit(t, "x86-64-linux", src)
				asmPath := filepath.Join(dir, "x86-"+m.name+".s")
				binPath := filepath.Join(dir, "x86-"+m.name)
				if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
					t.Fatalf("write asm: %v", err)
				}
				if out, err := exec.Command(x86gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
					t.Fatalf("gcc: %v\n%s", err, out)
				}
				cmd := runX86_64Bin(x86runner, binPath)
				_ = cmd.Run()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("%s did not exit normally", m.name)
				}
				if code := cmd.ProcessState.ExitCode(); code != 0 {
					t.Errorf("exit = %d, want 0 — %d of the matrix's combinations took the wrong branch", code, code)
				}
			})
		}
	})

	t.Run("arm64", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		for _, sc := range shape {
			t.Run("shape-"+sc.name, func(t *testing.T) {
				body := fusedFnBody(t, emit(t, "arm64-linux", sc.src), "f")
				for _, w := range sc.wantArm {
					if !strings.Contains(body, w) {
						t.Errorf("missing fused %q in:\n%s", w, body)
					}
				}
				for _, d := range sc.deadArm {
					if strings.Contains(body, d) {
						t.Errorf("un-fused %q still emitted in:\n%s", d, body)
					}
				}
			})
		}
		for _, m := range []struct {
			name     string
			ty       string
			unsigned bool
			pairs    [][2]uint64
		}{
			{"matrix-signed", "i32", false, signedPairs},
			{"matrix-unsigned", "u64", true, unsignedPairs},
		} {
			t.Run(m.name, func(t *testing.T) {
				src := cmpBranchMatrix(m.ty, m.unsigned, m.pairs)
				asm := emit(t, "arm64-linux", src)
				asmPath := filepath.Join(dir, "arm64-"+m.name+".s")
				binPath := filepath.Join(dir, "arm64-"+m.name)
				if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
					t.Fatalf("write asm: %v", err)
				}
				if out, err := exec.Command(armgcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
					t.Fatalf("gcc: %v\n%s", err, out)
				}
				cmd := runArm64Bin(qemu, binPath)
				_ = cmd.Run()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("%s did not exit normally", m.name)
				}
				if code := cmd.ProcessState.ExitCode(); code != 0 {
					t.Errorf("exit = %d, want 0 — %d of the matrix's combinations took the wrong branch", code, code)
				}
			})
		}
	})
}

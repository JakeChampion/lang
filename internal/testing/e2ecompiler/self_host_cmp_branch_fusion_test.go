package e2ecompiler

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

var cmpOps = []string{"<", "<=", ">", ">=", "==", "!="}

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

func cmpBranchMatrix(ty string, unsigned bool, pairs [][2]uint64) string {
	var b strings.Builder
	var checks []string
	id := 0
	b.WriteString("@noinline function sq(x: i32): i32 { return x * x; }\n")
	b.WriteString("@noinline function one(): i32 { return 1; }\n")
	for _, op := range cmpOps {
		for _, nots := range []int{0, 1, 2} {
			cond := strings.Repeat("!", nots) + "(a " + op + " b)"
			fmt.Fprintf(&b, "function fi%d(a: %s, b: %s): i32 { if (%s) { return 1; } return 0; }\n", id, ty, ty, cond)
			fmt.Fprintf(&b, "function fe%d(a: %s, b: %s): i32 { if (%s) { return 1; } else { return 0; } }\n", id, ty, ty, cond)
			fmt.Fprintf(&b, "function fc%d(a: %s, b: %s): i32 { if (%s) { return one(); } return 0; }\n", id, ty, ty, cond)
			fmt.Fprintf(&b, "function fw%d(a: %s, b: %s): i32 { let n: i32 = 0; while (%s) { n = n + 1; if (n > 0) { return n; } } return 0; }\n", id, ty, ty, cond)
			for _, p := range pairs {
				want := 0
				if cmpEval(op, p[0], p[1], unsigned) != (nots%2 == 1) {
					want = 1
				}
				for _, fn := range []string{"fi", "fe", "fc", "fw"} {
					checks = append(checks, fmt.Sprintf("%s%d(%s, %s) - %d",
						fn, id, cmpLit(p[0], unsigned), cmpLit(p[1], unsigned), want))
				}
			}
			id++
		}
	}
	checks = append(checks, cmpBooleanBranchChecks(&b)...)
	b.WriteString("function main(): i32 {\n  let bad: i32 = 0;\n")
	for _, c := range checks {
		fmt.Fprintf(&b, "  bad = bad + sq(%s);\n", c)
	}
	b.WriteString("  return bad;\n}\n")
	return b.String()
}

func cmpBooleanBranchChecks(b *strings.Builder) []string {
	var checks []string
	for _, nots := range []int{0, 1, 2} {
		cond := strings.Repeat("!", nots) + "a"
		fmt.Fprintf(b, "@noinline function bi%d(a: boolean): i32 { if (%s) { return 1; } return 0; }\n", nots, cond)
		fmt.Fprintf(b, "@noinline function be%d(a: boolean): i32 { if (%s) { return 1; } else { return 0; } }\n", nots, cond)
		fmt.Fprintf(b, "@noinline function bc%d(a: boolean): i32 { if (%s) { return one(); } return 0; }\n", nots, cond)
		fmt.Fprintf(b, "@noinline function bw%d(a: boolean): i32 { while (%s) { return 1; } return 0; }\n", nots, cond)
		for _, value := range []bool{false, true} {
			want := 0
			if value != (nots%2 == 1) {
				want = 1
			}
			for _, fn := range []string{"bi", "be", "bc", "bw"} {
				checks = append(checks, fmt.Sprintf("%s%d(%t) - %d", fn, nots, value, want))
			}
		}
	}
	return checks
}

var signedPairs = [][2]uint64{
	{uint64(1), uint64(2)},
	{uint64(2), uint64(2)},
	{uint64(5), uint64(2)},
	{uint64(0xFFFFFFFFFFFFFFFD), uint64(2)},
}

var unsignedPairs = [][2]uint64{
	{1, 2},
	{2, 2},
	{5, 2},
	{18000000000000000000, 2},
}

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

	shape := []struct {
		name             string
		src              string
		wantX86, deadX86 []string
		wantArm, deadArm []string
	}{
		{
			name:    "signed",
			src:     "@noinline function two(): i32 { return 2; }\n@noinline function f(a: i32, b: i32): i32 { if (a < b) { return 1; } return two(); }\nfunction main(): i32 { return f(1, 2); }",
			wantX86: []string{`cmpq %r[a-z0-9]+, %r[a-z0-9]+\n    jge `},
			deadX86: []string{"setl", "movzbq"},
			wantArm: []string{`cmp x[0-9]+, x[0-9]+\n    b.ge `},
			deadArm: []string{"cset"},
		},
		{
			name:    "unsigned",
			src:     "@noinline function two(): i32 { return 2; }\n@noinline function f(a: u64, b: u64): i32 { if (a < b) { return 1; } return two(); }\nfunction main(): i32 { return f(1, 2); }",
			wantX86: []string{`cmpq %r[a-z0-9]+, %r[a-z0-9]+\n    jae `},
			deadX86: []string{"setb", "movzbq"},
			wantArm: []string{`cmp x[0-9]+, x[0-9]+\n    b.hs `},
			deadArm: []string{"cset"},
		},
		{
			name:    "not-only",
			src:     "@noinline function two(): i32 { return 2; }\n@noinline function f(a: boolean): i32 { if (!a) { return 1; } return two(); }\nfunction main(): i32 { return f(false); }",
			wantX86: []string{`testq %r[a-z0-9]+, %r[a-z0-9]+\n    jn?z `},
			deadX86: []string{"setz", "movzbq"},
			wantArm: []string{`cbn?z x[0-9]+, `},
			deadArm: []string{"cset"},
		},
		{
			name:    "loop-guard",
			src:     "@noinline function f(n: i32): i32 { let i: i32 = 0; let c: i32 = 0; while (i < n) { c = c + 2; i = i + 1; } return c; }\nfunction main(): i32 { return f(3); }",
			wantX86: []string{`cmpq %r[a-z0-9]+, %r[a-z0-9]+\n    jge `},
			deadX86: []string{"setl", "setz"},
			wantArm: []string{`cmp x[0-9]+, x[0-9]+\n    b.ge `},
			deadArm: []string{"cset"},
		},
	}

	t.Run("x86-64", func(t *testing.T) {
		for _, sc := range shape {
			t.Run("shape-"+sc.name, func(t *testing.T) {
				body := fusedFnBody(t, emit(t, "x86-64-linux", sc.src), "f")
				for _, w := range sc.wantX86 {
					if !matchShape(body, w) {
						t.Errorf("missing fused %q in:\n%s", w, body)
					}
				}
				for _, d := range sc.deadX86 {
					if matchShape(body, d) {
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
					if !matchShape(body, w) {
						t.Errorf("missing fused %q in:\n%s", w, body)
					}
				}
				for _, d := range sc.deadArm {
					if matchShape(body, d) {
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

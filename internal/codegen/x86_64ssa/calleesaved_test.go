package x86_64ssa_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// calleeSavedNames are the System V callee-saved general-purpose registers, with
// every width spelling the emitter can produce for each.
var calleeSavedNames = map[string][]string{
	"rbx": {"rbx", "ebx", "bx", "bl"},
	"r12": {"r12", "r12d", "r12w", "r12b"},
	"r13": {"r13", "r13d", "r13w", "r13b"},
	"r14": {"r14", "r14d", "r14w", "r14b"},
	"r15": {"r15", "r15d", "r15w", "r15b"},
}

// Tokens keep `_` and `.` as word characters so a label such as
// `.Lssa_strcat_bl` stays one word rather than decomposing into `bl`.
var wordRe = regexp.MustCompile(`[A-Za-z_.][A-Za-z0-9_.]*`)

// regsIn returns the canonical callee-saved register names mentioned on a line,
// matched as whole words so `r12` does not also match inside `r12d`'s parse or
// inside a label.
func regsIn(line string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(line, -1) {
		for canon, spellings := range calleeSavedNames {
			for _, s := range spellings {
				if w == s {
					out[canon] = true
				}
			}
		}
	}
	return out
}

// emitAsm compiles src through the SSA backend and returns the assembly text.
func emitAsm(t *testing.T, src string) string {
	t.Helper()
	p, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(p)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	monomorph.Run(p, info)
	asm, err := x86.EmitProgram(p, info, 10)
	if err != nil {
		t.Fatalf("EmitProgram: %v", err)
	}
	return asm
}

// TestCalleeSavedCoverage checks the saved set from outside the emitter.
//
// calleeSavedIn scans the text the emitter just produced, which is complete by
// construction — but only for the pass that produced it. This asserts the
// property on the FINISHED function instead: for every function, each
// callee-saved register mentioned anywhere in the body must be one the prologue
// saved. So it catches a probe pass that drifts out of step with the real one
// (a helper emitting a register only on the second pass, or a save whose slot
// or ordering is wrong), which the scan alone cannot see.
//
// It matters because the failure is silent. Dropping a save does not break the
// compile; it returns a clobbered register to the caller and surfaces as a wrong
// answer somewhere unrelated.
//
// Deliberately stricter than the ABI — a register the function only reads still
// has to be saved here. That direction fails loudly rather than quietly.
func TestCalleeSavedCoverage(t *testing.T) {
	srcs := map[string]string{
		"leaf":        `function f(a: i32): i32 { return a + 1; } function main(): i32 { return f(7); }`,
		"loop":        `function f(xs: i32[]): i32 { var s: i32 = 0; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; } function main(): i32 { var a: i32[] = [1,2,3]; return f(a); }`,
		"calls":       `function g(a: i32, b: i32): i32 { return a * b; } function f(a: i32): i32 { return g(a, 2) + g(a, 3); } function main(): i32 { return f(5); }`,
		"nested_if":   `function f(x: i32): i32 { if (x < 10) { return x*x; } if (x < 100) { return x + 7; } return x - 3; } function main(): i32 { return f(42); }`,
		"many_locals": `function f(a: i32): i32 { var b = a+1; var c = b+2; var d = c+3; var e = d+4; var g = e+5; var h = g+6; var i = h+7; var j = i+8; return a+b+c+d+e+g+h+i+j; } function main(): i32 { return f(1); }`,
		"option":      `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`,
		"strings":     `function f(a: string, b: string): i32 { var c: string = a + b; return c.len(); } function main(): i32 { return f("ab", "cd"); }`,
	}

	// Prologue saves look like `mov [rbp - 24], rbx` — a store INTO a frame slot
	// whose source is a callee-saved register.
	saveRe := regexp.MustCompile(`^\s*mov \[rbp - \d+\], (rbx|r1[2-5])$`)

	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			asm := emitAsm(t, src)

			var fn string
			var savedSet map[string]bool
			inPrologue := false
			checked := 0

			for _, line := range strings.Split(asm, "\n") {
				trimmed := strings.TrimSpace(line)
				// A function label: `fn_<name>:` at column 0.
				if strings.HasPrefix(line, "fn_") && strings.HasSuffix(trimmed, ":") {
					fn = strings.TrimSuffix(trimmed, ":")
					savedSet = map[string]bool{}
					inPrologue = true
					checked++
					continue
				}
				if fn == "" {
					continue
				}
				if m := saveRe.FindStringSubmatch(line); m != nil && inPrologue {
					savedSet[m[1]] = true
					continue
				}
				// The prologue ends at the first block label.
				if strings.HasPrefix(trimmed, ".L_") {
					inPrologue = false
				}
				for r := range regsIn(trimmed) {
					if !savedSet[r] {
						t.Errorf("%s: %s is used but never saved in the prologue\n    at: %s\n    saved: %v",
							fn, r, trimmed, keys(savedSet))
					}
				}
			}
			if checked == 0 {
				t.Fatal("no functions found in the emitted assembly — the parse above is wrong, not the emitter")
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCalleeSavedLeafSavesNothing pins the win directly: a leaf that touches no
// callee-saved register must emit no save/restore traffic at all.
//
// Stated as a property of the emitted text rather than a byte count, because the
// byte count is what TestCodeSizeMarginalPerFunction measures and this is the
// mechanism behind it. Before the fix this function saved and restored all five
// registers it never referenced — ten instructions around four of work.
func TestCalleeSavedLeafSavesNothing(t *testing.T) {
	asm := emitAsm(t, `function f(a: i32): i32 { return a + 1; } function main(): i32 { return f(7); }`)

	var body []string
	in := false
	for _, line := range strings.Split(asm, "\n") {
		if strings.HasPrefix(line, "fn_f:") {
			in = true
			continue
		}
		if in && strings.HasPrefix(line, "fn_") {
			break
		}
		if in {
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		t.Fatal("fn_f not found in the emitted assembly")
	}
	joined := strings.Join(body, "\n")
	for canon := range calleeSavedNames {
		if regsIn(joined)[canon] {
			t.Errorf("leaf f() references %s; it computes a + 1 and should touch no callee-saved register\n%s", canon, joined)
		}
	}
}

// TestCodeSizeMarginalPerFunction pins the MARGINAL cost of one more function,
// which is the figure that actually describes a real codebase.
//
// A ratio measured on a one-function program is dominated by fixed overhead and
// says nothing about a program with hundreds. That is not hypothetical: the
// same reduce shape measures 54% of the stack machine at n=1 and 144% at n=100,
// because the two backends differ by a CONSTANT per function. Only the slope
// makes that visible, so this measures the slope — compile the same shape at two
// sizes and divide the difference.
//
// Both sides come from the in-process assembler, so the numbers repeat exactly
// and a tight tolerance cannot flake. Widen it only with a re-measurement.
func TestCodeSizeMarginalPerFunction(t *testing.T) {
	gen := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "function fn%d(xs: i32[]): i32 { var s: i32 = %d; var i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }\n", i, i)
		}
		b.WriteString("function main(): i32 { var xs: i32[] = [1,2,3]; var s: i32 = 0;\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "  s = s + fn%d(xs);\n", i)
		}
		b.WriteString("  return s; }\n")
		return b.String()
	}

	const lo, hi = 20, 60
	ssaLo, smLo := textSizes(t, gen(lo))
	ssaHi, smHi := textSizes(t, gen(hi))

	ssaPer := float64(ssaHi-ssaLo) / float64(hi-lo)
	smPer := float64(smHi-smLo) / float64(hi-lo)

	// Measured with the shipping pipeline — EmitProgram runs ssa.Optimize +
	// ssa.Verify since #6979: SSA 243 B/fn, stack machine 178 B/fn on this
	// shape. Two things moved it down from #6956's 250: Optimize is worth
	// 4 B/fn here and dropping dead 64-bit self-moves another 3. The ceiling
	// tightens with each, since a bound left above what the backend actually
	// emits is slack by exactly the difference.
	const wantSSAPer = 243.0
	if ssaPer > wantSSAPer+1 {
		t.Errorf("SSA marginal cost %.0f B/fn is above the pinned %.0f — a per-function emit regression",
			ssaPer, wantSSAPer)
	}
	t.Logf("marginal per function: SSA %.0f B  stack-machine %.0f B  (SSA %.0f%% of SM)",
		ssaPer, smPer, ssaPer/smPer*100)
}

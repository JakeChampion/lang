package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

type edgeType struct {
	name   string
	signed bool
	bits   uint
}

// usize has the values of 32 bits, which each target holds.
var edgeTypes = []edgeType{
	{"u8", false, 8}, {"u16", false, 16}, {"i16", true, 16}, {"i32", true, 32},
	{"u32", false, 32}, {"i64", true, 64}, {"u64", false, 64}, {"usize", false, 32},
}

// edgeMax is the largest number of a type.
func edgeMax(t edgeType) uint64 {
	top := uint64(1) << (t.bits - 1)
	if t.signed {
		return top - 1
	}
	return top - 1 + top
}

// edgeValues are the numbers at the two ends of a type, the numbers next to
// them, and the shift counts at and next to the width.
func edgeValues(t edgeType) []string {
	max := edgeMax(t)
	vals := []string{"0", "1", "2", "7", fmt.Sprint(max), fmt.Sprint(max - 1), fmt.Sprint(max/2 + 1), fmt.Sprint(t.bits), fmt.Sprint(t.bits - 1), fmt.Sprint(t.bits + 1)}
	if t.signed {
		vals = append(vals, "-1", "-2", "-7", "-"+fmt.Sprint(max), "min")
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// edgeLit writes a value as an expression of type t. A negative literal of
// an unsigned type does not exist, and the least number of a signed type has
// no positive form, so each negative number is a subtraction from 0.
func edgeLit(t edgeType, v string) string {
	if v == "min" {
		return fmt.Sprintf("(0 as %s - (%d as %s) - (1 as %s))", t.name, edgeMax(t), t.name, t.name)
	}
	if strings.HasPrefix(v, "-") {
		return fmt.Sprintf("(0 as %s - (%s as %s))", t.name, v[1:], t.name)
	}
	return fmt.Sprintf("(%s as %s)", v, t.name)
}

// integerEdgeProgram prints one line for each type and operator. A "run"
// line reads its operands through a function that the compiler does not
// inline, and a "const" line reads them from locals that the compiler can
// fold. Each cast prints the two forms with "=" between them.
func integerEdgeProgram(ops []string, optional bool, casts string) string {
	var b strings.Builder
	b.WriteString("import \"std/string\";\n")
	for _, t := range edgeTypes {
		fmt.Fprintf(&b, "@noinline function id_%s(n: %s): %s { return n; }\n", t.name, t.name, t.name)
		fmt.Fprintf(&b, "function show_%s(o: Option[%s]): string { match (o) { Some(v) => { return v.to_string(); }, None => { return \"N\"; } } return \"N\"; }\n", t.name, t.name)
	}
	var calls []string
	next := func() string {
		name := fmt.Sprintf("fn_%d", len(calls))
		calls = append(calls, name)
		return name
	}
	show := func(t edgeType, e string, opt bool) string {
		if opt {
			return fmt.Sprintf("show_%s(%s)", t.name, e)
		}
		return fmt.Sprintf("(%s).to_string()", e)
	}
	for _, t := range edgeTypes {
		vs := edgeValues(t)
		for _, op := range ops {
			// A saturating or a checked operator refuses usize: its limit depends on the target.
			if t.name == "usize" && len(op) > 1 && (strings.HasSuffix(op, "|") || strings.HasSuffix(op, "?")) {
				continue
			}
			for _, mode := range []string{"run", "const"} {
				fmt.Fprintf(&b, "function %s(): void {\n  let out = \"%s %s %s:\";\n", next(), t.name, mode, op)
				for i, x := range vs {
					for j, y := range vs {
						e := fmt.Sprintf("id_%s(%s) %s id_%s(%s)", t.name, edgeLit(t, x), op, t.name, edgeLit(t, y))
						if mode == "const" {
							fmt.Fprintf(&b, "  let a%d_%d: %s = %s;\n  let b%d_%d: %s = %s;\n", i, j, t.name, edgeLit(t, x), i, j, t.name, edgeLit(t, y))
							e = fmt.Sprintf("a%d_%d %s b%d_%d", i, j, op, i, j)
						}
						fmt.Fprintf(&b, "  out = out + \" \" + %s;\n", show(t, e, optional))
					}
				}
				b.WriteString("  print(out);\n}\n")
			}
		}
		for _, u := range edgeTypes {
			// A cast to usize keeps the bits of the pointer width, which is not the same on each target.
			if casts == "" || u.name == t.name || u.name == "usize" {
				continue
			}
			fmt.Fprintf(&b, "function %s(): void {\n  let out = \"%s %s %s:\";\n", next(), t.name, casts, u.name)
			for k, x := range vs {
				fmt.Fprintf(&b, "  let c%d: %s = %s;\n", k, t.name, edgeLit(t, x))
				fmt.Fprintf(&b, "  out = out + \" \" + %s + \"=\" + %s;\n",
					show(u, fmt.Sprintf("id_%s(%s) %s %s", t.name, edgeLit(t, x), casts, u.name), casts == "as?"),
					show(u, fmt.Sprintf("c%d %s %s", k, casts, u.name), casts == "as?"))
			}
			b.WriteString("  print(out);\n}\n")
		}
	}
	b.WriteString("function main(): i32 {\n")
	for _, c := range calls {
		fmt.Fprintf(&b, "  %s();\n", c)
	}
	b.WriteString("  return 0;\n}\n")
	return b.String()
}

// The operators and casts of each integer type give one result on each
// engine, at the ends of the type and at the shift counts next to its width:
// the compiler's interpreter, x86-64, arm64 and wasm. A result that the
// compiler folds is the result that the program computes.
func TestSelfHostIntegerEdgeMatrix(t *testing.T) {
	cli := buildSelfHostCLI(t)
	programs := map[string]string{
		"wrapping":   integerEdgeProgram([]string{"+", "-", "*", "/", "%", "<<", ">>", "&", "|", "^"}, false, "as"),
		"saturating": integerEdgeProgram([]string{"+|", "-|", "*|", "<<|"}, false, ""),
		"checked":    integerEdgeProgram([]string{"+?", "-?", "*?", "/?", "%?", "<<?", ">>?"}, true, "as?"),
	}
	for name, program := range programs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			want := runSelfHostInterp(t, "main.fern", program)
			checkFoldedLines(t, "interp", want)
			outputs := map[string]*exec.Cmd{
				"x86-64-linux": runX86_64Bin(cli.runner, cli.x86Binary(t, src)),
				"wasm32-wasi":  exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src)),
			}
			_, qemu := arm64Tooling(t)
			outputs["arm64-linux"] = runArm64Bin(qemu, cli.arm64Binary(t, src))
			for target, cmd := range outputs {
				got, err := cmd.Output()
				if err != nil {
					t.Fatalf("%s: %v", target, err)
				}
				checkFoldedLines(t, target, string(got))
				expected := want
				if target == "wasm32-wasi" {
					expected = wasmEdgeExpected(t, want)
				}
				if line := firstDifferentLine(expected, string(got)); line != "" {
					t.Errorf("%s and the interpreter differ:\n%s", target, line)
				}
			}
		})
	}
}

// wasm32 usize operations have the same width and test operands as u32.
// Casts from usize already have portable results: every input fits in 32 bits.
func wasmEdgeExpected(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	u32 := map[string]string{}
	for _, line := range lines {
		head, results, ok := strings.Cut(line, ":")
		if op, found := strings.CutPrefix(head, "u32 "); found && ok {
			u32[op] = results
		}
	}
	for i, line := range lines {
		head, _, _ := strings.Cut(line, ":")
		op, found := strings.CutPrefix(head, "usize ")
		if !found || strings.HasPrefix(op, "as") {
			continue
		}
		results, ok := u32[op]
		if !ok {
			t.Fatalf("missing u32 reference for %s", head)
		}
		lines[i] = head + ":" + results
	}
	return strings.Join(lines, "\n")
}

// checkFoldedLines compares each "const" line with its "run" line, and the
// two forms of each cast.
func checkFoldedLines(t *testing.T, engine, out string) {
	t.Helper()
	run := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		head, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.Contains(head, " run ") {
			run[strings.Replace(head, " run ", " ", 1)] = rest
		}
		if strings.Contains(head, " const ") {
			if key := strings.Replace(head, " const ", " ", 1); run[key] != rest {
				t.Errorf("%s: %s: the folded results are not the computed results\n%s\n%s", engine, key, rest, run[key])
			}
		}
		if strings.Contains(head, " as") {
			for _, pair := range strings.Fields(rest) {
				if a, b, _ := strings.Cut(pair, "="); a != b {
					t.Errorf("%s: %s: the folded cast gives %s and the computed cast gives %s", engine, head, b, a)
				}
			}
		}
	}
}

// firstDifferentLine gives the first line that two outputs do not share,
// with the first result that differs.
func firstDifferentLine(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range w {
		if i >= len(g) {
			return "the output ends before: " + w[i]
		}
		if w[i] == g[i] {
			continue
		}
		wf, gf := strings.Fields(w[i]), strings.Fields(g[i])
		for k := range wf {
			if k >= len(gf) || wf[k] != gf[k] {
				head, _, _ := strings.Cut(w[i], ":")
				gotField := "nothing"
				if k < len(gf) {
					gotField = gf[k]
				}
				return fmt.Sprintf("%s: result %d is %s in the interpreter and %s here", head, k, wf[k], gotField)
			}
		}
		return w[i]
	}
	return ""
}

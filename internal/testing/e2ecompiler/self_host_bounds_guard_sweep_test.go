package e2ecompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// guardGen writes a function of guards, loops and index arithmetic over five
// arrays and views. About half of its guards are sufficient for the access
// that they guard. The others look sufficient: they bound a different
// expression, a different array, or a sum that wraps.
type guardGen struct {
	r     *rand.Rand
	depth int
	names int
}

func (g *guardGen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func guardLit(n int64) string {
	if n == -2147483648 {
		return "(0 - 2147483647 - 1)"
	}
	if n < 0 {
		return fmt.Sprintf("(0 - %d)", -n)
	}
	return strconv.FormatInt(n, 10)
}

func (g *guardGen) small() string {
	return g.pick("0", "1", "1", "1", "2", "3", "4", "7", "8", "15", "16", "17")
}

func (g *guardGen) atom(scope []string) string {
	k := g.r.Float64()
	if k < 0.65 {
		return g.pick(scope...)
	}
	if k < 0.85 {
		return g.pick("xs", "ys", "bs", "v", "sv", "ws") + ".len()"
	}
	consts := []int64{0, 1, 2, 3, 15, 16, -1, -2, 2147483647, -2147483648, 2147483646, 65536, 1073741824}
	return guardLit(consts[g.r.Intn(len(consts))])
}

func (g *guardGen) index(scope []string, d int) string {
	k := g.r.Float64()
	a := g.atom(scope)
	switch {
	case d > 1 || k < 0.35:
		return a
	case k < 0.55:
		return a + " + " + g.small()
	case k < 0.70:
		return a + " - " + g.small()
	case k < 0.76:
		return a + " * " + g.pick("2", "3", "4", "8", "65536")
	case k < 0.82:
		return "(" + a + " & " + g.pick("1", "3", "7", "15", "255", "65535") + ")"
	case k < 0.86:
		return "(" + a + " >> " + g.pick("1", "2", "31") + ")"
	case k < 0.90:
		return a + " / " + g.pick("2", "4", "3")
	case k < 0.93:
		return a + " % " + g.pick("2", "4", "3", "16")
	case k < 0.97:
		return "(" + g.index(scope, d+1) + ") + (" + g.index(scope, d+1) + ")"
	}
	return g.pick("xs", "bs", "v") + ".len() - 1 - " + a
}

func (g *guardGen) arr() string  { return g.pick("xs", "xs", "ys", "v", "ws", "ws") }
func (g *guardGen) barr() string { return g.pick("bs", "bs", "sv") }

// guard is a condition about the span elements from index e of array a.
func (g *guardGen) guard(scope []string, e, a string, d, span int) string {
	k := g.r.Float64()
	n := a + ".len()"
	if d == 0 && g.r.Float64() < 0.5 {
		last := ""
		if span > 1 {
			last = fmt.Sprintf(" - %d", span-1)
		}
		forms := []string{
			fmt.Sprintf("%s >= 0 && %s < %s%s", e, e, n, last),
			fmt.Sprintf("%s >= 0 && %s <= %s - %d", e, e, n, span),
			fmt.Sprintf("0 <= %s && %s%s > %s", e, n, last, e),
			fmt.Sprintf("%s > 0 - 1 && %s + %d <= %s && %s < %s", e, e, span, n, e, n),
		}
		if span == 1 {
			forms = append(forms, fmt.Sprintf("(%s) as u32 < %s as u32", e, n))
		}
		return g.pick(forms...)
	}
	other := g.pick("xs", "ys", "bs", "v", "sv", "ws") + ".len()"
	c := g.small()
	if d < 2 && k < 0.22 {
		left := g.guard(scope, e, a, d+1, 1)
		return left + " " + g.pick("&&", "&&", "||") + " " + g.guard(scope, g.pick(e, g.index(scope, 0)), a, d+1, 1)
	}
	if d < 2 && k < 0.27 {
		return "!(" + g.guard(scope, e, a, d+1, 1) + ")"
	}
	return g.pick(
		e+" < "+n, e+" < "+n, e+" >= 0", e+" >= 0",
		e+" <= "+n, e+" < "+other,
		"("+e+") as u32 < "+n+" as u32",
		"("+e+") as u32 <= "+n+" as u32",
		e+" + "+c+" < "+n, e+" + "+c+" <= "+n,
		e+" < "+n+" - "+c, e+" <= "+n+" - "+c,
		e+" - "+c+" < "+n, e+" > 0 - 1", e+" > 0",
		e+" != "+n, e+" == "+g.atom(scope),
		n+" > "+e, n+" >= "+e+" + "+c, n+" - "+e+" > "+c,
		g.index(scope, 0)+" "+g.pick("<", "<=", ">", ">=", "==", "!=")+" "+g.index(scope, 0),
	)
}

// read is an i32 expression that reads span elements from index e of array a.
func (g *guardGen) read(scope []string) (x, e, a string, span int) {
	k := g.r.Float64()
	e = g.index(scope, 0)
	switch {
	case k < 0.55:
		a = g.arr()
		return a + "[" + e + "]", e, a, 1
	case k < 0.70:
		a = g.barr()
		return "(" + a + "[" + e + "] as i32)", e, a, 1
	case k < 0.82:
		a = g.barr()
		span = []int{2, 4, 4, 8}[g.r.Intn(4)]
		var parts []string
		for i := 0; i < span; i++ {
			parts = append(parts, fmt.Sprintf("((%s[%s + %d] as i32) << %d)", a, e, i, (8*i)%32))
		}
		return "(" + strings.Join(parts, " + ") + ")", e, a, span
	case k < 0.92:
		return "lane(bs, " + e + ")", e, "bs", 16
	}
	return "lanev(sv, " + e + ")", e, "sv", 16
}

func (g *guardGen) stmt(scope []string, out *[]string, ind int, inLoop bool) {
	g.depth++
	defer func() { g.depth-- }()
	pad := strings.Repeat("    ", ind)
	add := func(format string, args ...any) { *out = append(*out, pad+fmt.Sprintf(format, args...)) }
	k := g.r.Float64()
	if g.depth > 6 {
		k = 0
	}
	switch {
	case k < 0.30:
		x, e, a, span := g.read(scope)
		cond := g.guard(scope, e, a, 0, span)
		if g.r.Float64() < 0.06 {
			add("acc = acc + %s;", x)
		} else if g.r.Float64() < 0.3 {
			leave := "return acc;"
			if inLoop && g.r.Float64() < 0.5 {
				leave = "break;"
			}
			add("if (!(%s)) { %s }", cond, leave)
			add("acc = acc + %s;", x)
		} else {
			add("if (%s) {", cond)
			add("    acc = acc + %s;", x)
			if g.r.Float64() < 0.4 {
				g.stmt(scope, out, ind+1, inLoop)
			}
			add("}")
		}
	case k < 0.45:
		e := g.index(scope, 0)
		src := g.pick("ws", "ws", "xs", "ys")
		if g.r.Float64() < 0.6 {
			rd := g.pick(src, src, "xs", "ys", "ws", "v")
			re := g.pick(e, e, g.index(scope, 0))
			if g.r.Float64() < 0.7 {
				add("if (!(%s)) { return acc; }", g.guard(scope, re, rd, 0, 1))
			}
			add("acc = acc + %s[%s];", rd, re)
		}
		if g.r.Float64() < 0.6 {
			add("if (%s) {", g.guard(scope, e, src, 0, 1))
			add("    ws = %s.with(%s, acc);", src, e)
			add("}")
		} else {
			add("ws = %s.with(%s, acc);", src, e)
		}
	case k < 0.52:
		if g.r.Float64() < 0.6 {
			add("ws = ws.append(acc);")
		} else {
			add("ws = %s;", g.pick("xs", "ys"))
		}
	case k < 0.58:
		e := g.index(scope, 0)
		cond := g.guard(scope, e, "wb", 0, 16)
		if g.r.Float64() < 0.9 {
			add("if (%s) { wb = simd.with_u8x16(wb, %s, simd.load_u8x16(bs, 0)); }", cond, e)
		} else {
			add("wb = simd.with_u8x16(wb, %s, simd.load_u8x16(bs, 0));", e)
		}
		add("acc = acc + (wb[0] as i32);")
	case k < 0.66:
		lo, hi := g.index(scope, 0), g.index(scope, 0)
		a := g.pick("xs", "v", "ws", "ys")
		g.names++
		name := fmt.Sprintf("w%d", g.names)
		if g.r.Float64() < 0.6 {
			add("if (%s >= 0 && %s <= %s && %s <= %s.len()) {", lo, lo, hi, hi, a)
		} else if g.r.Float64() < 0.6 {
			add("if (%s && %s) {", g.guard(scope, lo, a, 0, 1), g.guard(scope, hi, a, 0, 1))
		} else {
			add("{")
		}
		add("    let %s: [i32] = %s[%s:%s];", name, a, lo, hi)
		add("    acc = acc + %s.len();", name)
		e := g.index(scope, 0)
		if g.r.Float64() < 0.85 {
			add("    if (%s) { acc = acc + %s[%s]; }", g.guard(scope, e, g.pick(name, a), 0, 1), name, e)
		} else {
			add("    acc = acc + %s[%s];", name, e)
		}
		add("}")
	default:
		g.names++
		j := fmt.Sprintf("j%d", g.names)
		lo := g.pick("0", "0", "0", "1", g.index(scope, 0), "a", "b")
		hi := g.pick(g.arr()+".len()", g.barr()+".len()", g.barr()+".len() - "+g.small(), g.arr()+".len() + 1", "a", "b", g.index(scope, 0))
		cmp := g.pick("<", "<", "<", "<=", "!=")
		step := g.pick("1", "1", "1", "2", "4", "16")
		down := g.r.Float64() < 0.15
		inner := append(append([]string{}, scope...), j)
		var body []string
		for n := 1 + g.r.Intn(3); n > 0; n-- {
			g.stmt(inner, &body, ind+1, true)
		}
		if g.r.Float64() < 0.15 {
			body = append(body, fmt.Sprintf("%s    %s = %s + %s;", pad, j, j, g.small()))
		}
		switch {
		case down:
			add("let %s: i32 = %s - 1;", j, hi)
			add("while (%s %s %s) {", j, g.pick(">=", ">", ">="), g.pick("0", lo, "0 - 1"))
			*out = append(*out, body...)
			add("    %s = %s - %s;", j, j, step)
		case g.r.Float64() < 0.3:
			add("for (let %s: i32 = %s; %s %s %s; %s = %s + %s) {", j, lo, j, cmp, hi, j, j, step)
			*out = append(*out, body...)
		default:
			add("let %s: i32 = %s;", j, lo)
			add("while (%s %s %s) {", j, cmp, hi)
			*out = append(*out, body...)
			add("    %s = %s + %s;", j, j, step)
		}
		add("}")
	}
}

const guardLaneBody = `    let o: u8[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
    let p: u8[] = simd.with_u8x16(o, 0, simd.%s(bs, i));
    return (p[0] as i32) + (p[15] as i32) * 256;
}
`

// boundsGuardProgram is the program of one seed. Its main calls the function
// with indexes at the ends of its arrays and at the ends of i32.
func boundsGuardProgram(seed int64) string {
	g := &guardGen{r: rand.New(rand.NewSource(seed))}
	pickInt := func(xs ...int) int { return xs[g.r.Intn(len(xs))] }
	nx := pickInt(0, 1, 2, 3, 5, 8, 16, 17)
	ny := pickInt(0, 1, 4, 9, 33)
	nb := pickInt(0, 1, 3, 15, 16, 17, 31, 32, 40)
	var b strings.Builder
	b.WriteString("import \"std/i32\";\nimport \"std/simd\";\n\n")
	// A lane helper that the compiler can inline puts the lane access in the
	// function with the guards.
	keep := "@noinline\n"
	if g.r.Float64() < 0.5 {
		keep = ""
	}
	b.WriteString(keep + "function lane(bs: u8[], i: i32): i32 {\n" + fmt.Sprintf(guardLaneBody, "load_u8x16"))
	b.WriteString(keep + "function lanev(bs: [u8], i: i32): i32 {\n" + fmt.Sprintf(guardLaneBody, "load_view_u8x16"))
	first := "xs"
	if nx > 0 {
		first = g.pick("xs", "ys", "xs.with(0, 5)")
	} else {
		first = g.pick("xs", "ys")
	}
	b.WriteString("@noinline\nfunction f(xs: i32[], ys: i32[], bs: u8[], v: [i32], sv: [u8], a: i32, b: i32): i32 {\n")
	b.WriteString("    let acc: i32 = 0;\n    let ws: i32[] = " + first + ";\n    let wb: u8[] = bs;\n")
	var body []string
	for n := 1 + g.r.Intn(3); n > 0; n-- {
		g.depth = 0
		g.stmt([]string{"a", "b"}, &body, 1, false)
	}
	b.WriteString(strings.Join(body, "\n"))
	b.WriteString("\n    return acc + ws.len() + (wb.len() << 8);\n}\n")
	b.WriteString(`function build(n: i32): i32[] {
    let r: i32[] = [];
    let i: i32 = 0;
    while (i < n) { r = r.append(i * 3 + 1); i = i + 1; }
    return r;
}
function bytes(n: i32): u8[] {
    let r: u8[] = [];
    let i: i32 = 0;
    while (i < n) { r = r.append((i * 7 + 2) as u8); i = i + 1; }
    return r;
}
function main(): i32 {
`)
	vlo := g.r.Intn(nx + 1)
	vhi := vlo + g.r.Intn(nx-vlo+1)
	slo := g.r.Intn(nb + 1)
	shi := slo + g.r.Intn(nb-slo+1)
	fmt.Fprintf(&b, "    let xs: i32[] = build(%d);\n    let ys: i32[] = build(%d);\n    let bs: u8[] = bytes(%d);\n", nx, ny, nb)
	fmt.Fprintf(&b, "    let v: [i32] = xs[%d:%d];\n    let sv: [u8] = bs[%d:%d];\n", vlo, vhi, slo, shi)
	edges := []int64{0, 0, 1, 1, 2, 3, -1, -2, -16, -17, 15, 16, 17, int64(nx), int64(nx - 1), int64(nx + 1),
		int64(nb), int64(nb - 1), int64(nb + 1), int64(nb - 15), int64(nb - 16), int64(nb - 17), int64(ny), int64(ny - 1),
		int64(vhi - vlo), int64(shi - slo), 2147483647, 2147483646, -2147483648, -2147483647, 65536, 1073741824, 2147483632, 2147483631}
	for n := 3 + g.r.Intn(5); n > 0; n-- {
		fmt.Fprintf(&b, "    print(f(xs, ys, bs, v, sv, %s, %s).to_string());\n", guardLit(edges[g.r.Intn(len(edges))]), guardLit(edges[g.r.Intn(len(edges))]))
	}
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

// guardRun is what one engine does with a program: its stdout, and the cause
// line of its abort, or "" when it ends without one.
type guardRun struct {
	stdout string
	cause  string
}

func runGuardProgram(t *testing.T, cmd *exec.Cmd) (guardRun, bool) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err == nil {
		return guardRun{stdout: out.String()}, true
	}
	for _, line := range strings.Split(errb.String(), "\n") {
		if strings.HasPrefix(line, "fern: ") {
			return guardRun{stdout: out.String(), cause: line}, true
		}
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() < 0 {
		return guardRun{}, false
	}
	t.Fatalf("%v: %v\n%s", cmd.Args, err, errb.String())
	return guardRun{}, false
}

// Each optimized target does what the interpreter does with a program of
// guards and index arithmetic: the same lines, and the same abort or none.
// The interpreter checks each access. The targets remove the checks that
// compiler/ssabounds.fern and compiler/semversion.fern prove, so a wrong
// proof is a line that differs or an abort that does not occur. Each target
// runs a second build that keeps those checks, which shows whether a
// difference comes from a proof.
// FERN_BOUNDS_SWEEP gives the number of programs, and FERN_BOUNDS_SWEEP_BASE
// the seed before the first.
func TestSelfHostBoundsGuardSweep(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interp := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	wasmtime := e2eharness.Wasmtime(t)
	_, qemu := arm64Tooling(t)
	programs := guardSweepSetting(t, "FERN_BOUNDS_SWEEP", 32)
	base := int64(guardSweepSetting(t, "FERN_BOUNDS_SWEEP_BASE", 0))
	compared := make(chan bool, programs)
	t.Run("programs", func(t *testing.T) {
		for seed := base + 1; seed <= base+int64(programs); seed++ {
			t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
				t.Parallel()
				program := boundsGuardProgram(seed)
				src := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				// A loop of the program can run to the end of i32 in the interpreter.
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				want, ended := runGuardProgram(t, exec.CommandContext(ctx, interp, "-interp", src, stdlib))
				if !ended {
					compared <- false
					if ctx.Err() == context.DeadlineExceeded {
						t.Skip("interpreter exceeded the ten-second limit")
					}
					t.Fatal("interpreter did not produce a verdict")
				}
				compared <- true
				engines := map[string]*exec.Cmd{
					"x86-64-linux":                  runX86_64Bin(cli.runner, cli.x86Binary(t, src)),
					"arm64-linux":                   runArm64Bin(qemu, cli.arm64Binary(t, src)),
					"wasm32-wasi":                   exec.Command(wasmtime, "run", cli.wasmComponent(t, src)),
					"x86-64-linux, no range proofs": runX86_64Bin(cli.runner, cli.x86Binary(t, src, noRangeProofs)),
					"arm64-linux, no range proofs":  runArm64Bin(qemu, cli.arm64Binary(t, src, noRangeProofs)),
					"wasm32-wasi, no range proofs":  exec.Command(wasmtime, "run", cli.wasmComponent(t, src, noRangeProofs)),
				}
				for target, cmd := range engines {
					got, ended := runGuardProgram(t, cmd)
					if !ended {
						t.Fatalf("%s did not produce a verdict", target)
					}
					if got != want {
						t.Errorf("%s: stdout %q, cause %q\ninterpreter: stdout %q, cause %q\n%s", target, got.stdout, got.cause, want.stdout, want.cause, program)
					}
				}
			})
		}
	})
	close(compared)
	reached := 0
	for ok := range compared {
		if ok {
			reached++
		}
	}
	if reached*4 < programs*3 {
		t.Errorf("%d of %d programs ended in the interpreter, want three of four", reached, programs)
	}
}

func guardSweepSetting(t *testing.T, name string, standard int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return standard
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}

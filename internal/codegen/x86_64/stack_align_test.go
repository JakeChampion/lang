package x86_64

// Static proof that every `call` this backend emits sees a 16-byte-aligned
// rsp, as System V AMD64 requires (and as the `__c_call*` FFI shims hand
// straight to arbitrary C code, which may use aligned SSE).
//
// The operand stack uses 8-byte slots, so alignment is no longer a property
// of the slot size — it is maintained by the generator's opBytes tracking and
// the pad callAligned / emitCallArgsLoad emit. A missed rsp adjustment there
// would be invisible in generated Fern-to-Fern code (nothing in it faults on
// a misaligned stack) and would only surface as a crash in a C callee, so the
// gate cannot be "the tests still pass".
//
// checkStackAlignment therefore re-derives rsp from the emitted text itself
// rather than trusting the counter that produced it: it walks each function's
// instructions, simulates every rsp movement modulo 16, propagates across
// branches, and reports any `call` reached at an odd multiple of 8.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// alignState is rsp mod 16 at a program point, or unknown after an
// unconditional transfer of control.
type alignState struct {
	mod   int
	known bool
}

// checkStackAlignment returns one message per problem found in `asm`.
func checkStackAlignment(asm string) []string {
	var problems []string
	var (
		fn     string // current function symbol
		cur    alignState
		frame  alignState      // rsp mod 16 when `mov rbp, rsp` last ran
		labels map[string]int  // label -> rsp mod 16 expected there
		seen   map[string]bool // labels whose expectation is pinned
	)
	reset := func(name string, entry int) {
		fn = name
		cur = alignState{mod: entry, known: true}
		frame = alignState{}
		labels = map[string]int{}
		seen = map[string]bool{}
	}
	reset("<toplevel>", 0)

	note := func(format string, args ...any) {
		problems = append(problems, fn+": "+fmt.Sprintf(format, args...))
	}
	// expect records that control reaches `label` with rsp mod 16 == m.
	expect := func(label string, m int) {
		if prev, ok := labels[label]; ok {
			if prev != m && seen[label] {
				note("label %s reached at rsp%%16=%d and %d", label, prev, m)
			}
			return
		}
		labels[label] = m
	}

	inText := true
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			// A label or a directive.
			if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, ".") {
				name := strings.TrimSuffix(line, ":")
				// A global function label starts a fresh frame: entry rsp is
				// 8 mod 16 (the caller's `call` pushed the return address).
				// `_start` is entered by the kernel with rsp 16-aligned.
				if name == "_start" {
					reset(name, 0)
				} else {
					reset(name, 8)
				}
				continue
			}
			if strings.HasSuffix(line, ":") {
				name := strings.TrimSuffix(line, ":")
				if m, ok := labels[name]; ok {
					cur = alignState{mod: m, known: true}
				} else if cur.known {
					labels[name] = cur.mod
				}
				seen[name] = true
				continue
			}
			switch {
			case strings.HasPrefix(line, ".section .rodata"), strings.HasPrefix(line, ".section .bss"),
				strings.HasPrefix(line, ".data"), strings.HasPrefix(line, ".bss"):
				inText = false
			case strings.HasPrefix(line, ".text"):
				inText = true
			}
			continue
		}
		if !inText {
			continue
		}
		ins := strings.TrimSpace(line)
		if i := strings.Index(ins, "//"); i >= 0 {
			ins = strings.TrimSpace(ins[:i])
		}
		mnemonic, rest, _ := strings.Cut(ins, " ")
		rest = strings.TrimSpace(rest)

		switch mnemonic {
		case "push":
			cur.mod = (cur.mod + 8) % 16
		case "pop":
			cur.mod = (cur.mod + 8) % 16
			if rest == "rsp" {
				cur.known = false
			}
		case "sub", "add":
			dst, src, ok := strings.Cut(rest, ",")
			if !ok || strings.TrimSpace(dst) != "rsp" {
				break
			}
			n, err := strconv.Atoi(strings.TrimSpace(src))
			if err != nil {
				cur.known = false
				break
			}
			if n%8 != 0 {
				note("rsp adjusted by %d, not a multiple of 8", n)
			}
			if mnemonic == "sub" {
				cur.mod = ((cur.mod-n)%16 + 16) % 16
			} else {
				cur.mod = ((cur.mod + n) % 16) % 16
			}
		case "mov", "lea":
			dst, src, ok := strings.Cut(rest, ",")
			if !ok || strings.TrimSpace(dst) != "rsp" {
				break
			}
			if mnemonic == "mov" && strings.TrimSpace(src) == "rbp" {
				// The epilogue restores the frame's own alignment.
				cur = frame
				break
			}
			cur.known = false
		case "and", "or", "xor", "shl", "shr":
			if !strings.HasPrefix(rest, "rsp") {
				break
			}
			if mnemonic == "and" && strings.TrimSpace(strings.TrimPrefix(rest, "rsp,")) == "-16" {
				// Alignment taken outright, not preserved.
				cur = alignState{mod: 0, known: true}
				break
			}
			cur.known = false
		case "call":
			if cur.known && cur.mod != 0 {
				note("call %s at rsp%%16=%d", rest, cur.mod)
			}
		case "jmp":
			if cur.known && isLocalLabel(rest) {
				expect(rest, cur.mod)
			}
			cur.known = false
		case "ret":
			cur.known = false
		default:
			if strings.HasPrefix(mnemonic, "j") && isLocalLabel(rest) {
				// Conditional branch: the target sees the current alignment
				// and so does the fall-through.
				if cur.known {
					expect(rest, cur.mod)
				}
			}
		}
		if mnemonic == "mov" {
			if dst, src, ok := strings.Cut(rest, ","); ok &&
				strings.TrimSpace(dst) == "rbp" && strings.TrimSpace(src) == "rsp" {
				frame = cur
			}
		}
	}
	return problems
}

func isLocalLabel(s string) bool {
	return s != "" && !strings.ContainsAny(s, " ,[]") && !strings.HasPrefix(s, "*")
}

// alignPrograms exercises the shapes where the operand stack goes deep, goes
// odd, or interleaves with raw pushes: nested calls, an odd-arity struct
// literal, a >6-arg call (the stack-overflow arg path), closures with captures
// (raw push r12/r13 over live operand slots), trait objects (OpBoxDyn's raw
// push rbx/r12), float math (fbinPop plus a helper call), and string concat.
var alignPrograms = map[string]string{
	"deep_expr": `
function f(a: i32, b: i32, c: i32): i32 { return a * b + c; }
function main(): i32 {
  return f(f(1, 2, 3), f(4, 5, 6), f(7, 8, f(9, 1, 2))) + f(1, f(2, 3, 4), 5);
}`,

	"odd_fields": `
struct S { a: i32, b: i32, c: i32, d: i32, e: i32 }
function mk(n: i32): S { return S { a: n, b: n + 1, c: n + 2, d: n + 3, e: n + 4 }; }
function main(): i32 { var s = mk(1); return s.a + s.b + s.c + s.d + s.e; }`,

	"nine_args": `
function g(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, h: i32, i: i32, j: i32): i32 {
  return a + b + c + d + e + f + h + i + j;
}
function main(): i32 { return g(1, 2, 3, 4, 5, 6, 7, 8, g(1, 1, 1, 1, 1, 1, 1, 1, 1)); }`,

	"closures": `
function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
  var a: i32 = 1;
  var b: i32 = 2;
  var c: i32 = 3;
  var g = (y: i32): i32 => { return y + a + b + c; };
  return apply(g, 4) + apply((z: i32): i32 => { return z + a; }, 5);
}`,

	"strings": `
function main(): i32 {
  var s: string = "ab" + "cd";
  var t: string = s + "ef" + s;
  if (t == "abcdefabcd") { return 1; }
  return 0;
}`,

	"floats": `
function main(): i32 {
  var x: f64 = 1.5;
  var y: f64 = 2.25;
  var z: f64 = x * y + x / y - x;
  if (z > 0.0) { return 1; }
  return 0;
}`,

	"arrays": `
function main(): i32 {
  var a: i32[] = [1, 2, 3, 4, 5];
  var t: i32 = 0;
  var i: i32 = 0;
  while (i < 5) { t = t + a[i]; i = i + 1; }
  return t;
}`,
}

func TestEmittedCallsAre16ByteAligned(t *testing.T) {
	for name, src := range alignPrograms {
		t.Run(name, func(t *testing.T) {
			asm := compile(t, src)
			if problems := checkStackAlignment(asm); len(problems) > 0 {
				for _, p := range problems {
					t.Error(p)
				}
			}
		})
	}
}

// The pad exists only where it is needed, so a program whose operand depth is
// always even must not pay for it. This is the other half of the contract:
// without it, "always pad" would pass the alignment check while giving back
// the size win the 8-byte slot bought.
func TestNoPadWhereAlignmentAlreadyHolds(t *testing.T) {
	asm := compile(t, `
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(40, 2); }`)
	body, ok := fnBodyOf(asm, "main")
	if !ok {
		t.Fatal("main not found in emitted asm")
	}
	if strings.Contains(body, "sub rsp, 8") {
		t.Errorf("main paid for an alignment pad it does not need:\n%s", body)
	}
}

// The whole examples/ corpus, checked the same way — far more shapes than a
// handful of inline programs can cover, and it costs no toolchain.
func TestExampleCorpusCallsAre16ByteAligned(t *testing.T) {
	root := filepath.Join("..", "..", "..", "examples")
	files, err := filepath.Glob(filepath.Join(root, "*.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no examples found under %s", root)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			asm, err := compileMaybe(string(src))
			if err != nil {
				t.Skipf("not compilable standalone: %v", err)
			}
			for _, p := range checkStackAlignment(asm) {
				t.Error(p)
			}
		})
	}
}

// compileMaybe is compile() for sources that may legitimately not build on
// their own (an example that imports a module, say) — the caller skips those
// rather than failing.
func compileMaybe(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	info, err := checker.Check(prog)
	if err != nil {
		return "", err
	}
	if err := monomorph.Run(prog, info); err != nil {
		return "", err
	}
	return Emit(prog, info)
}

// fnBodyOf slices the emitted text between `name:` and its `.size` directive.
func fnBodyOf(asm, name string) (string, bool) {
	start := strings.Index(asm, "\n"+name+":\n")
	if start < 0 {
		return "", false
	}
	rest := asm[start:]
	end := strings.Index(rest, "\n.size "+name+",")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// The checker must actually be able to fail — a verifier that reports nothing
// on hand-broken input proves nothing about the emitter.
func TestStackAlignmentCheckerDetectsMisalignment(t *testing.T) {
	bad := strings.Join([]string{
		".text",
		"f:",
		"\tpush rbp",
		"\tmov rbp, rsp",
		"\tsub rsp, 8",
		"\tcall g",
		"\tmov rsp, rbp",
		"\tpop rbp",
		"\tret",
	}, "\n")
	if got := checkStackAlignment(bad); len(got) == 0 {
		t.Error("checker accepted a call at a misaligned rsp")
	}
	good := strings.Replace(bad, "\tsub rsp, 8\n", "\tsub rsp, 16\n", 1)
	if got := checkStackAlignment(good); len(got) != 0 {
		t.Errorf("checker rejected an aligned call: %v", got)
	}
}

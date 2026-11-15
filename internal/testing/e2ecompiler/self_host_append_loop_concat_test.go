package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An index loop whose only effect is a = a.append(b[i]) becomes one range
// concat (#15829). These cases run with the leak census and the sanitizer,
// and the function that holds the loop must call the concat kernel and
// keep no loop.
var appendLoopConcatCases = []struct {
	name, fn, program string
}{
	{"u8", "cat_bytes", `@noinline function cat_bytes(own a: u8[], b: [u8], lo: i32, hi: i32): u8[] {
  let i = lo;
  while (i < hi) { a = a.append(b[i]); i += 1; }
  return a;
}
function main(): i32 {
  let src = __alloc_u8(args().len() + 199);
  for k in 0..src.len() { src = src.with(k, (k * 7 + 1) as u8); }
  let base: u8[] = [9];
  let kept = base;
  let out = cat_bytes(base, src, 3, 180);
  if (out.len() != 178 || out[0] != 9u8 || out[1] != 22u8 || out[177] != src[179]) { return 1; }
  if (kept.len() != 1 || kept[0] != 9u8) { return 2; }
  if (cat_bytes([], src, 7, 2).len() != 0 || cat_bytes([1u8], src, 400, 400).len() != 1) { return 3; }
  let big: u8[] = [];
  for k in 0..300 { big = cat_bytes(big, src, 0, 200); }
  if (big.len() != 60000 || big[59999] != src[199]) { return 4; }
  return 0;
}`},
	{"i32", "cat_words", `@noinline function cat_words(own a: i32[], b: i32[], lo: i32, hi: i32): i32[] {
  for i in lo..hi { a = a.append(b[i]); }
  return a;
}
function main(): i32 {
  let w: i32[] = [];
  for k in 0..args().len() + 63 { w = w.append(k * 3); }
  let base: i32[] = [7];
  let kept = base;
  let out = cat_words(base, w, 1, 40);
  if (out.len() != 40 || out[0] != 7 || out[1] != 3 || out[39] != 117) { return 1; }
  if (kept.len() != 1 || kept[0] != 7) { return 2; }
  if (cat_words([], w, 9, 3).len() != 0) { return 3; }
  let grown: i32[] = [];
  for k in 0..100 { grown = cat_words(grown, w, 0, 64); }
  if (grown.len() != 6400 || grown[6399] != 189) { return 4; }
  return 0;
}`},
	{"boolean", "cat_flags", `@noinline function cat_flags(own a: boolean[], b: [boolean], lo: i32, hi: i32): boolean[] {
  for i in lo..hi { a = a.append(b[i]); }
  return a;
}
function main(): i32 {
  let src: boolean[] = [];
  for k in 0..args().len() + 63 { src = src.append(k % 3 == 0); }
  let out = cat_flags([false], src, 1, 40);
  if (out.len() != 40 || out[0] || out[1] || !out[3] || !out[39]) { return 1; }
  let grown: boolean[] = [];
  for k in 0..50 { grown = cat_flags(grown, src, 0, 64); }
  if (grown.len() != 3200 || grown[3198] || !grown[3199]) { return 2; }
  return 0;
}`},
	{"f64", "cat_reals", `@noinline function cat_reals(own a: f64[], b: f64[], lo: i32, hi: i32): f64[] {
  let i = lo;
  while (i < hi) { a = a.append(b[i]); i += 1; }
  return a;
}
function main(): i32 {
  let src: f64[] = [];
  for k in 0..args().len() + 15 { src = src.append(k as f64 * 0.5); }
  let out = cat_reals([9.0], src, 2, 16);
  if (out.len() != 15 || out[0] != 9.0 || out[1] != 1.0 || out[14] != 7.5) { return 1; }
  return 0;
}`},
	{"string", "cat_texts", `@noinline function cat_texts(own a: string[], b: string[], lo: i32, hi: i32): (string[], i32) {
  let i = lo;
  while (i < hi) { a = a.append(b[i]); i += 1; }
  return (a, i);
}
@noinline function texts(p: string, n: i32): string[] {
  let out: string[] = [];
  for k in 0..n { out = out.append(p + k.to_string()); }
  return out;
}
function main(): i32 {
  let src = texts("s", 6);
  let base = texts("b", 2);
  let kept = base;
  let (out, i) = cat_texts(base, src, 1, 5);
  if (out.len() != 6 || out[1] != "b1" || out[2] != "s1" || out[5] != "s4" || i != 5) { return 1; }
  if (kept.len() != 2 || kept[1] != "b1" || src[4] != "s4") { return 2; }
  let (empty, j) = cat_texts(texts("e", 1), src, 4, 2);
  if (empty.len() != 1 || j != 4) { return 3; }
  let grown: string[] = [];
  for k in 0..40 {
    let (g, _) = cat_texts(grown, src, 0, 6);
    grown = g;
  }
  if (grown.len() != 240 || grown[239] != "s5" || src[5] != "s5") { return 4; }
  return 0;
}`},
	{"record", "cat_recs", `struct Rec { name: string, n: i32 }
@noinline function cat_recs(own a: Rec[], b: [Rec]): Rec[] {
  for r in b { a = a.append(r); }
  return a;
}
@noinline function recs(n: i32): Rec[] {
  let out: Rec[] = [];
  for k in 0..n { out = out.append(Rec { name: "r" + k.to_string(), n: k }); }
  return out;
}
function main(): i32 {
  let src = recs(5);
  let base = recs(1);
  let kept = base;
  let out = cat_recs(base, src);
  if (out.len() != 6 || out[0].name != "r0" || out[5].name != "r4" || out[5].n != 4) { return 1; }
  if (kept.len() != 1 || src[2].name != "r2") { return 2; }
  let grown: Rec[] = [];
  for k in 0..30 { grown = cat_recs(grown, src); }
  if (grown.len() != 150 || grown[149].name != "r4") { return 3; }
  return 0;
}`},
}

func TestSelfHostAppendLoopConcat(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range appendLoopConcatCases {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(c.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, c.program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
				body := emittedFunction(t, cli, c.program, target, c.fn)
				if !strings.Contains(body, "__fern_arr_concat") {
					t.Errorf("%s does not call the concat kernel:\n%s", c.fn, body)
				}
				if hasLoop(body, target) {
					t.Errorf("%s keeps a loop:\n%s", c.fn, body)
				}
			})
		}
	}
}

// Each of these loops does more than append b[i] for each i, so each keeps
// its loop and gives the result the loop gives.
const appendLoopKeptProgram = `@noinline function reads_a(own a: i32[], b: i32[]): i32[] {
  let i = 0;
  while (i < b.len()) { a = a.append(b[i] + a.len()); i += 1; }
  return a;
}
@noinline function changes_b(own a: i32[], b: i32[], n: i32): i32[] {
  let i = 0;
  while (i < n) { a = a.append(b[i]); b = b.append(9); i += 1; }
  return a;
}
@noinline function step2(own a: i32[], b: i32[]): i32[] {
  let i = 0;
  while (i < b.len()) { a = a.append(b[i]); i += 2; }
  return a;
}
@noinline function transform(own a: i32[], b: i32[]): i32[] {
  for i in 0..b.len() { a = a.append(b[i] + 1); }
  return a;
}
@noinline function breaks(own a: i32[], b: i32[]): i32[] {
  for i in 0..b.len() { if (b[i] == 3) { break; } a = a.append(b[i]); }
  return a;
}
@noinline function same(a: i32[]): i32[] {
  let x = a;
  let n = a.len();
  let i = 0;
  while (i < n) { x = x.append(a[i]); i += 1; }
  return x;
}
function main(): i32 {
  let w: i32[] = [1, 2, 3, 4];
  let r1 = reads_a([5], w);
  if (r1.len() != 5 || r1[1] != 2 || r1[4] != 8) { return 1; }
  let r2 = changes_b([], w, args().len() + 2);
  if (r2.len() != 3 || r2[2] != 3 || w.len() != 4) { return 2; }
  let r3 = step2([], w);
  if (r3.len() != 2 || r3[1] != 3) { return 3; }
  let r4 = transform([], w);
  if (r4.len() != 4 || r4[0] != 2 || r4[3] != 5) { return 4; }
  let r5 = breaks([], w);
  if (r5.len() != 2 || r5[1] != 2) { return 5; }
  let r6 = same([7, 8]);
  if (r6.len() != 4 || r6[2] != 7 || r6[3] != 8) { return 6; }
  return 0;
}`

func TestSelfHostAppendLoopKeepsLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, appendLoopKeptProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			for _, fn := range []string{"reads_a", "changes_b", "step2", "transform", "breaks", "same"} {
				body := emittedFunction(t, cli, appendLoopKeptProgram, target, fn)
				if strings.Contains(body, "__fern_arr_concat") || !hasLoop(body, target) {
					t.Errorf("%s lost its loop:\n%s", fn, body)
				}
			}
		})
	}
}

// A range past either end of the source aborts as the first read past it
// does. An empty or reversed range reads nothing.
func TestSelfHostAppendLoopConcatBounds(t *testing.T) {
	const functions = `@noinline function cat_words(own a: i32[], b: [i32], lo: i32, hi: i32): i32[] {
  let i = lo;
  while (i < hi) { a = a.append(b[i]); i += 1; }
  return a;
}
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for name, call := range map[string]string{
			"past-the-end":   "cat_words([], w, 2, 5)",
			"negative-start": "cat_words([], w, 0 - 1, 2)",
			"wrapping-count": "cat_words([], w, 0 - 2147483000, 2147483000)",
		} {
			t.Run(target+"/"+name, func(t *testing.T) {
				program := functions + "function main(): i32 { let w: i32[] = [1, 2, 3, 4]; let out = " + call + "; return out.len(); }"
				stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1")
				if code == 0 {
					t.Fatal("out-of-bounds range returned")
				}
				if target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Fatalf("bounds: exit %d, want array abort 134\n%s", code, stderr)
				}
			})
		}
		t.Run(target+"/empty", func(t *testing.T) {
			program := functions + `function main(): i32 {
  let w: i32[] = [1, 2, 3, 4];
  if (cat_words([5], w, 9, 9).len() != 1 || cat_words([], w, 3, 0 - 7).len() != 0) { return 1; }
  if (cat_words([], w, 2147483000, 0 - 2147483000).len() != 0) { return 2; }
  return 0;
}`
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
		})
	}
}

// A concat only reads its source, so the source stays sole: a later append
// to it pushes in place with no test of its count.
func TestSelfHostConcatSourceStaysSole(t *testing.T) {
	const program = `import "std/array";
@noinline function grow(n: i32): i32 {
  let b: i32[] = [];
  for k in 0..n { b = b.append(k); }
  let a: i32[] = [9];
  a = a.concat(b);
  for k in 0..n { b = b.append(k); }
  return a.len() * 1000 + b.len();
}
function main(): i32 {
  if (grow(args().len() + 4) != 6010) { return 1; }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	stderr, code := cli.exitOf(t, program, "x86-64-linux", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	assertBalancedCensus(t, stderr)
	body := emittedFunction(t, cli, program, "x86-64-linux", "grow")
	if regexp.MustCompile(`(?m)^\s+call __fern_arr_push\s*$`).MatchString(body) || !strings.Contains(body, "__fern_arr_push_owned") {
		t.Errorf("an append after the concat tests the count of its sole receiver:\n%s", body)
	}
}

// concat on counted elements is the kernel too. Its result holds a count on
// each element, whether the receiver is shared, borrowed, a record field or
// a unique array that grows.
func TestSelfHostConcatCountedElements(t *testing.T) {
	const program = `import "std/array";
struct Box { xs: string[], n: i32 }
struct Rec { name: string }
@noinline function texts(p: string, n: i32): string[] {
  let out: string[] = [];
  for k in 0..n { out = out.append(p + k.to_string()); }
  return out;
}
@noinline function joined(a: string[], b: string[]): string[] {
  return a.concat(b);
}
@noinline function owned(own a: string[], b: [string]): string[] {
  return a.concat(b);
}
@noinline function in_field(s: Box, b: string[], k: i32): Box {
  let out = s;
  for j in 0..k { out = Box { ...out, xs: out.xs.concat(b) }; }
  return out;
}
@noinline function records(own a: Rec[], b: Rec[]): Rec[] {
  return a.concat(b);
}
function main(): i32 {
  let a = texts("a", 3);
  let b = texts("b", 2);
  let j = joined(a, b);
  if (j.len() != 5 || j[0] != "a0" || j[4] != "b1" || a.len() != 3 || b[1] != "b1") { return 1; }
  let kept = texts("k", 1);
  let alias = kept;
  let o = owned(kept, b);
  if (o.len() != 3 || o[2] != "b1" || alias.len() != 1) { return 2; }
  let grown: string[] = [];
  for k in 0..50 { grown = owned(grown, b); }
  if (grown.len() != 100 || grown[99] != "b1") { return 3; }
  let s = Box { xs: texts("s", 1), n: 4 };
  let g = in_field(s, b, 9);
  if (g.xs.len() != 19 || g.xs[18] != "b1" || s.xs.len() != 1) { return 4; }
  let rs: Rec[] = [Rec { name: "x" + "1" }];
  let rr = records(records([], rs), rs);
  if (rr.len() != 2 || rr[1].name != "x1" || rs[0].name != "x1") { return 5; }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			for _, fn := range []string{"joined", "owned", "in_field", "records"} {
				if body := emittedFunction(t, cli, program, target, fn); !strings.Contains(body, "__fern_arr_concat") {
					t.Errorf("%s does not call the concat kernel:\n%s", fn, body)
				}
			}
		})
	}
}

// The free function std/array.concat takes the kernel as the method does.
func TestSelfHostArrayConcatFunctionKernel(t *testing.T) {
	const program = `import "std/array";
@noinline function join_bytes(own a: u8[], b: [u8]): u8[] {
  return array.concat(a, b);
}
@noinline function join_texts(a: string[], b: string[]): string[] {
  return array.concat(a, b);
}
function main(): i32 {
  let src: u8[] = [1, 2, 3];
  let out = join_bytes([9], src);
  if (out.len() != 4 || out[0] != 9u8 || out[3] != 3u8) { return 1; }
  let a: string[] = ["x" + "1"];
  let b: string[] = ["y" + "2", "z" + "3"];
  let t = join_texts(a, b);
  if (t.len() != 3 || t[2] != "z3" || a.len() != 1 || b[0] != "y2") { return 2; }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			for _, fn := range []string{"join_bytes", "join_texts"} {
				body := emittedFunction(t, cli, program, target, fn)
				if !strings.Contains(body, "__fern_arr_concat") || strings.Contains(body, "array__concat") {
					t.Errorf("%s does not fold array.concat to the kernel:\n%s", fn, body)
				}
			}
		})
	}
}

// emittedFunction is the assembly or WAT of one function of program.
func emittedFunction(t *testing.T, cli *selfHostCLI, program, target, fn string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(cli.emit(t, src, target))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(text), "\n")
	start, end := "__fn_"+fn+":", ".cfi_endproc"
	if target == "wasm32-wasi" {
		start, end = "(func $"+fn+" ", "(func $"
	}
	var out []string
	for _, line := range lines {
		if len(out) == 0 {
			if strings.HasPrefix(strings.TrimSpace(line), start) {
				out = append(out, line)
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), end) {
			break
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		t.Fatalf("no function %s in the %s output", fn, target)
	}
	return strings.Join(out, "\n")
}

var ssaLabelRe = regexp.MustCompile(`\.Lssa_[A-Za-z0-9_]+`)

// hasLoop reports a WAT loop, or a branch to a label defined above it.
func hasLoop(body, target string) bool {
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if target == "wasm32-wasi" {
			if line == "loop" || strings.HasPrefix(line, "loop ") || strings.HasPrefix(line, "(loop") {
				return true
			}
			continue
		}
		if strings.HasSuffix(line, ":") {
			seen[strings.TrimSuffix(line, ":")] = true
			continue
		}
		for _, label := range ssaLabelRe.FindAllString(line, -1) {
			if seen[label] {
				return true
			}
		}
	}
	return false
}

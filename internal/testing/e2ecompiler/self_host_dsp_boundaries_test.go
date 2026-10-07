package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dspBoundaryHelpers = `
function near(a: f64, b: f64): boolean { let d: f64 = a - b; return d >= -0.000000000001 && d <= 0.000000000001; }
function same(a: f64[], b: f64[]): boolean { if (a.len() != b.len()) { return false; } let i: i32 = 0; while (i < a.len()) { if (!near(a[i], b[i])) { return false; } i = i + 1; } return true; }
function graph(n: i32, delay: i32): dsp_core.Graph { match (dsp_core.new_graph(n, delay)) { Some(g) => { return g; }, None => { exit(90); return dsp_core.Graph { capacity: 0, work: [], output: [], delay: [], cursor: 0, previous: 0.0, previous2: 0.0, samples: 0i64, output_count: 0, refused: true }; } } }
function snapshot(g: dsp_core.Graph): dsp_core.Graph { return g; }
function unchanged(a: dsp_core.Graph, b: dsp_core.Graph): boolean { return a.capacity == b.capacity && same(a.work, b.work) && same(a.output, b.output) && same(a.delay, b.delay) && a.cursor == b.cursor && a.samples == b.samples && near(a.previous, b.previous) && near(a.previous2, b.previous2); }
`

func TestSelfHostDSPBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, module := range []string{"dsp_fip", "dsp_direct"} {
		for _, tc := range []struct{ name, source string }{
			{"impulse_clip", `
function main(): i32 {
  let impulse: f64[] = [1.0, 0.0, 0.0, 0.0];
  let negative: f64[] = [-1.0, 0.0, 0.0, 0.0];
  let silent: f64[] = [0.0, 0.0, 0.0, 0.0];
  let expected: f64[] = [0.5625, 0.28125, 0.28125, 0.375];
  let expected_negative: f64[] = [-0.5625, -0.28125, -0.28125, -0.375];
  let ring: f64[] = [0.1875, 0.375, 0.375];
  let a: dsp_core.Graph = graph(4, 3);
  let b: dsp_core.Graph = graph(4, 3);
  let zero: dsp_core.Graph = graph(4, 1);
  a = SUBJECT.process(a, impulse, 4);
  b = SUBJECT.process(b, negative, 4);
  zero = SUBJECT.process(zero, silent, 4);
  if (!same(a.output, expected) || !same(b.output, expected_negative) || !same(a.delay, ring) || !same(zero.output, silent) || a.cursor != 1 || a.samples != 4i64) { return 1; }
  let saved: dsp_core.Graph = snapshot(a);
  a = SUBJECT.process(a, silent, 4);
  if (!same(saved.output, expected) || !same(saved.delay, ring) || saved.cursor != 1 || saved.samples != 4i64) { return 2; }
  let peaks: f64[] = [8.0, -8.0];
  let clip: dsp_core.Graph = graph(2, 1);
  clip = SUBJECT.process(clip, peaks, 2);
  if (clip.output[0] != 1.0 || clip.output[1] != 0.75 || dsp_core.limit(-8.0) != -1.0 || dsp_core.limit(-1.0) != -1.0 || dsp_core.limit(1.0) != 1.0) { return 3; }
  let bottom: dsp_core.Graph = graph(1, 1);
  bottom = SUBJECT.process(bottom, [-8.0], 1);
  if (bottom.output[0] != -1.0) { return 4; }
  return 0;
}`},
			{"refusals", `
function main(): i32 {
  let bad: i32[] = [-2147483648, -1, 0, 4097, 2147483647];
  let mark: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < bad.len()) { match (dsp_core.new_graph(bad[i], 3)) { Some(g) => { return 1; }, None => {} } i = i + 1; }
  if (__heap_alloc_count() != mark) { return 2; }
  let delays: i32[] = [-2147483648, -1, 0, 8193, 2147483647];
  mark = __heap_alloc_count(); i = 0;
  while (i < delays.len()) { match (dsp_core.new_graph(4, delays[i])) { Some(g) => { return 3; }, None => {} } i = i + 1; }
  if (__heap_alloc_count() != mark) { return 4; }
  let max: dsp_core.Graph = graph(4096, 8192);
  max = SUBJECT.process(max, [], 0);
  if (max.refused || max.output_count != 0 || max.samples != 0i64 || max.work.len() != 4096 || max.delay.len() != 8192) { return 5; }
  let a: dsp_core.Graph = graph(4, 3);
  a = SUBJECT.process(a, [1.0, 0.0], 2);
  let saved: dsp_core.Graph = snapshot(a);
  let counts: i32[] = [-2147483648, -1, 3, 5, 2147483647];
  i = 0;
  while (i < counts.len()) {
    a = SUBJECT.process(a, [1.0, 0.0], counts[i]);
    if (!a.refused || a.output_count != 0 || !unchanged(a, saved)) { return 6; }
    i = i + 1;
  }
  a = SUBJECT.process(a, [], 0);
  if (a.refused || a.output_count != 0 || !unchanged(a, saved)) { return 7; }
  a = dsp_core.Graph { ...a, samples: 9223372036854775806i64 };
  let limit_saved: dsp_core.Graph = snapshot(a);
  a = SUBJECT.process(a, [1.0, 0.0], 2);
  if (!a.refused || !unchanged(a, limit_saved)) { return 8; }
  a = SUBJECT.process(a, [0.0], 1);
  if (a.refused || a.samples != 9223372036854775807i64) { return 9; }
  let last: dsp_core.Graph = snapshot(a);
  a = SUBJECT.process(a, [], 0);
  if (a.refused || !unchanged(a, last)) { return 10; }
  a = SUBJECT.process(a, [0.0], 1);
  if (!a.refused || !unchanged(a, last)) { return 11; }
  return 0;
}`},
			{"partition", `
function main(): i32 {
  let input: f64[] = [1.0, 0.0, 0.0, 0.0, -2.0, 0.5, 4.0, -1.0, 0.0, 0.25, 0.0, 1.0, -0.5, 0.0, 0.0, 0.0];
  let p0: f64[] = [1.0]; let p1: f64[] = [0.0, 0.0, 0.0];
  let p2: f64[] = [-2.0, 0.5, 4.0, -1.0, 0.0];
  let p3: f64[] = [0.25, 0.0, 1.0, -0.5, 0.0, 0.0, 0.0];
  let whole: dsp_core.Graph = graph(16, 3);
  let part: dsp_core.Graph = graph(7, 3);
  whole = SUBJECT.process(whole, input, 16);
  part = SUBJECT.process(part, p0, 1);
  if (!near(part.output[0], whole.output[0])) { return 1; }
  part = SUBJECT.process(part, p1, 3);
  let i: i32 = 0;
  while (i < 3) { if (!near(part.output[i], whole.output[i + 1])) { return 2; } i = i + 1; }
  part = SUBJECT.process(part, p2, 5); i = 0;
  while (i < 5) { if (!near(part.output[i], whole.output[i + 4])) { return 3; } i = i + 1; }
  part = SUBJECT.process(part, p3, 7); i = 0;
  while (i < 7) { if (!near(part.output[i], whole.output[i + 9])) { return 4; } i = i + 1; }
  if (!same(part.delay, whole.delay) || part.cursor != whole.cursor || part.samples != whole.samples || !near(part.previous, whole.previous) || !near(part.previous2, whole.previous2)) { return 5; }
  return 0;
}`},
		} {
			t.Run(module+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				for _, name := range []string{"dsp_core.fern", module + ".fern"} {
					data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				source := fmt.Sprintf("import \"./dsp_core\";\nimport \"./%s\";\n", module) + dspBoundaryHelpers + strings.ReplaceAll(tc.source, "SUBJECT", module)
				path := filepath.Join(dir, "main.fern")
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
					t.Run(target, func(t *testing.T) {
						stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
						if code != 0 {
							t.Fatalf("exit %d: %s", code, stderr)
						}
						assertBalancedCensus(t, stderr)
					})
				}
			})
		}
	}
}

func TestSelfHostDSPRejectsAllocatingStage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(claim+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "main.fern")
				source := fmt.Sprintf("%s function stage(x: f64): f64[] { return [x * 1.5]; }\nfunction main(): i32 { return stage(1.0).len(); }\n", claim)
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E053") || !strings.Contains(string(out), "may not allocate (array literal)") {
					t.Fatalf("expected allocation-contract refusal, got %v: %s", err, out)
				}
			})
		}
	}
}

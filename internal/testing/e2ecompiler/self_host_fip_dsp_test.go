package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func dspLiteral(values []float64) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.Contains(parts[i], ".") {
			parts[i] += ".0"
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestSelfHostFipDSP(t *testing.T) {
	testDSPOracle(t, "dsp_fip")
}

func TestSelfHostDirectDSP(t *testing.T) {
	testDSPOracle(t, "dsp_direct")
}

func testDSPOracle(t *testing.T, module string) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name         string
		block, delay int
	}{
		{"pilot", 7, 3},
		{"single", 1, 1},
		{"short_delay", 64, 3},
		{"long_delay", 7, 67},
		{"large_block", 257, 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Scalar stream oracle: no staged buffers or subject helper calls.
			ring := make([]float64, tc.delay)
			cursor := 0
			previous, previous2 := 0.0, 0.0
			var fixtures, declarations, checks strings.Builder
			for block := 0; block < 5; block++ {
				input, output := make([]float64, tc.block), make([]float64, tc.block)
				for i := range input {
					index := block*tc.block + i
					x := float64(index*73%257-128) / 64
					if index%31 == 0 {
						x = 4
					} else if index%37 == 0 {
						x = -4
					} else if index%11 == 0 {
						x = 0
					}
					input[i] = x
					gained := x * 1.5
					filtered := gained*0.5 + previous*0.25 + previous2*0.25
					previous2, previous = previous, gained
					wet := ring[cursor]
					ring[cursor] = filtered + wet*0.25
					cursor = (cursor + 1) % len(ring)
					output[i] = min(1.0, max(-1.0, filtered*0.75+wet*0.5))
				}
				// Keep oracle literals in separate functions so GNU as can flush
				// each function's ARM literal pool. These execute before the mark.
				for _, fixture := range []struct {
					name   string
					values []float64
				}{{"input", input}, {"output", output}, {"ring", ring}} {
					fmt.Fprintf(&fixtures, "@noinline function fixture_%s%d(): f64[] { return %s; }\n", fixture.name, block, dspLiteral(fixture.values))
					fmt.Fprintf(&declarations, "let %s%d: f64[] = fixture_%s%d();\n", fixture.name, block, fixture.name, block)
				}
				fmt.Fprintf(&checks, "g = dsp_fip.process(g, input%d, %d);\nif (__heap_alloc_count() != mark) { return 10; }\nif (!same(g.output, output%d) || !same(g.delay, ring%d)) { return 11; }\nif (g.cursor != %d || g.samples != %di64 || g.output_count != %d || g.refused || !near(g.previous, %s) || !near(g.previous2, %s)) { return 12; }\n", block, tc.block, block, block, cursor, (block+1)*tc.block, tc.block, strconv.FormatFloat(previous, 'f', 12, 64), strconv.FormatFloat(previous2, 'f', 12, 64))
			}
			// Constants and input live outside the allocation mark. This includes
			// the FIRST callback after construction, without a warm-up exemption.
			source := `import "./dsp_core";
import "./dsp_fip";
` + fixtures.String() + `
function near(a: f64, b: f64): boolean { let d: f64 = a - b; return d >= -0.000000000001 && d <= 0.000000000001; }
function same(a: f64[], b: f64[]): boolean { if (a.len() != b.len()) { return false; } let i: i32 = 0; while (i < a.len()) { if (!near(a[i], b[i])) { return false; } i = i + 1; } return true; }
function main(): i32 {
` + declarations.String() + fmt.Sprintf("match (dsp_core.new_graph(%d, %d)) { None => { return 1; }, Some(g) => { let mark: i64 = __heap_alloc_count();\n", tc.block, tc.delay) + checks.String() + fmt.Sprintf(`
let saved: dsp_core.Graph = g;
g = dsp_fip.process(g, input0, %d);
if (!same(saved.output, output4) || !same(saved.delay, ring4) || saved.samples != %di64) { return 13; }
let before: dsp_core.Graph = g;
g = dsp_fip.process(g, input0, -1);
if (!g.refused || g.output_count != 0 || !same(g.delay, before.delay) || !same(g.output, before.output) || g.samples != before.samples || g.cursor != before.cursor || !near(g.previous, before.previous) || !near(g.previous2, before.previous2)) { return 14; }
return 0; } } }
`, tc.block, 5*tc.block)
			// The ordinary loop receives the same oracle, without imposing a
			// contract on its allocation count. The benchmark measures that count.
			source = strings.ReplaceAll(source, "dsp_fip", module)
			if module == "dsp_direct" {
				source = strings.ReplaceAll(source, "if (__heap_alloc_count() != mark) { return 10; }", "")
			}
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

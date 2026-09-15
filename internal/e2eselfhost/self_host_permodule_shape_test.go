package e2eselfhost

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostPerModuleShapeParser(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []pmModuleShape
		bad         bool
	}{
		{"empty", "", nil, false},
		{"ordered", "leaf|0\n__entry|2\n", []pmModuleShape{{"leaf", 0}, {"__entry", 2}}, false},
		{"missing separator", "leaf", nil, true},
		{"missing namespace", "|3", nil, true},
		{"missing count", "leaf|", nil, true},
		{"negative count", "leaf|-1", nil, true},
		{"noninteger", "leaf|one", nil, true},
		{"extra field", "leaf|1|2", nil, true},
		{"duplicate namespace", "leaf|1\nleaf|2", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePMModuleShape(tc.input)
			if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse = %#v, %v; want %#v, bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestSelfHostPerModuleShapeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "shape_driver")
	testPMModuleShape(t, func(entry, flag string) (string, error) {
		out, err := runX86_64Bin(runner, bin, entry, flag).CombinedOutput()
		return string(out), err
	})
}

func testPMModuleShape(t *testing.T, drive func(entry, flag string) (string, error)) {
	t.Helper()
	for _, tc := range []struct {
		name, leaf string
		functions  int
	}{
		{"plain", "pub function value(): i32 { return 5; }", 1},
		{"lifted capture", "pub function value(): i32 { var offset = 2; var f = (x: i32): i32 => x + offset; return f(3); }", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			for name, src := range map[string]string{
				"leaf.fern": tc.leaf,
				"main.fern": "import \"./leaf\"; function main(): i32 { return leaf.value(); }",
			} {
				if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(proj, "main.fern")
			query := func(flag string) string {
				t.Helper()
				out, err := drive(entry, flag)
				if err != nil {
					t.Fatalf("%s: %v\n%s", flag, err, out)
				}
				return strings.TrimSpace(out)
			}
			shape, err := parsePMModuleShape(query("-per-module-shape"))
			if err != nil {
				t.Fatal(err)
			}
			if len(shape) != 2 {
				t.Fatalf("shape has %d modules, want leaf and entry", len(shape))
			}
			n, err := strconv.Atoi(query("-per-module-count"))
			if err != nil || n != len(shape) {
				t.Fatalf("legacy module count = %d, %v; shape has %d", n, err, len(shape))
			}
			counts := strings.Fields(query("-per-module-func-counts"))
			var manifest []string
			for _, line := range strings.Split(query("-per-module-manifest"), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					manifest = append(manifest, line)
				}
			}
			if len(counts) != n || len(manifest) != n {
				t.Fatalf("legacy metadata lengths disagree: %d counts and %d manifest rows, want %d", len(counts), len(manifest), n)
			}
			foundLeaf := false
			for i, module := range shape {
				count, err := strconv.Atoi(counts[i])
				name, _, ok := strings.Cut(manifest[i], "|")
				if err != nil || !ok || count != module.functions || name != module.namespace {
					t.Fatalf("row %d = %#v, legacy count=%q manifest=%q", i, module, counts[i], manifest[i])
				}
				if module.namespace == "leaf" {
					foundLeaf = true
					if module.functions != tc.functions {
						t.Fatalf("leaf has %d functions, want %d after lambda lifting", module.functions, tc.functions)
					}
				}
			}
			if !foundLeaf {
				t.Fatal("leaf module missing")
			}
		})
	}
}

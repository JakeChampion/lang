package sourcelint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreutilsBenchHyperfineSchemas(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "scripts", "coreutils-bench"))
	if err != nil {
		t.Fatal(err)
	}
	python := func(marker string) string {
		t.Helper()
		_, rest, ok := strings.Cut(string(b), marker)
		if !ok {
			t.Fatalf("missing benchmark script marker %q", marker)
		}
		_, rest, ok = strings.Cut(rest, "<<'PY'\n")
		if !ok {
			t.Fatal("missing Python heredoc")
		}
		src, _, ok := strings.Cut(rest, "\nPY\n")
		if !ok {
			t.Fatal("unterminated Python heredoc")
		}
		return src
	}
	annotate := python(`python3 - "$json"`)
	report := python(`table=$(python3 -`)
	for _, schema := range []int{0, 2, 3} {
		t.Run(string(rune('0'+schema)), func(t *testing.T) {
			d := map[string]any{}
			if schema != 0 {
				d["schema_version"] = schema
			}
			var results []any
			for _, name := range []string{"fern", "gnu", "uutils"} {
				mean, code := 0.001, 0
				if name == "gnu" {
					mean = 0.002
				}
				if name == "uutils" {
					code = 1
				}
				r := map[string]any{"command": name, "mean": mean, "stddev": 0, "times": []float64{mean}, "exit_codes": []int{code}}
				if schema != 0 {
					r = map[string]any{
						"command": "/different/path/" + name + " --flag", "name": name,
						"summary":      map[string]any{"time_wall_clock": map[string]any{"unit": "second", "mean": mean, "stddev": 0}},
						"measurements": []any{map[string]any{"time_wall_clock": map[string]any{"value": mean, "unit": "second"}, "exit_code": code}},
					}
				}
				results = append(results, r)
			}
			d["results"] = results
			data, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "run.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "-c", annotate, path, "ls", "columns").CombinedOutput()
			if schema == 3 {
				if err == nil || !strings.Contains(string(out), "unsupported hyperfine schema") {
					t.Fatalf("unknown schema was not refused: %v\n%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("annotate: %v\n%s", err, out)
			}
			out, err = exec.Command("python3", "-c", report, path).CombinedOutput()
			if err != nil {
				t.Fatalf("report: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), "| `ls` | columns | 1.00 ± 0.00 (1 runs) | 2.00 ± 0.00 (1 runs) | exit 1 | 2.00× | — |") {
				t.Fatalf("report lost timings, ratio, sample count or refusal:\n%s", out)
			}
		})
	}
}

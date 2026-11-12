package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type jsonDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Length   int    `json:"length"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Related  []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		Length  int    `json:"length"`
		Message string `json:"message"`
	} `json:"related"`
}

func TestSelfHostDiagFormatJSON(t *testing.T) {
	t.Setenv("FERN_DIAG_FORMAT", "")
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(env string, args ...string) (string, int) {
		cmd := exec.Command(h.cli, args...)
		if env != "" {
			cmd.Env = append(os.Environ(), env)
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return stderr.String(), cmd.ProcessState.ExitCode()
	}
	decode := func(t *testing.T, out string) []jsonDiagnostic {
		t.Helper()
		var ds []jsonDiagnostic
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			var d jsonDiagnostic
			dec := json.NewDecoder(strings.NewReader(line))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&d); err != nil {
				t.Fatalf("line is not one diagnostic object: %v\n%q", err, line)
			}
			if d.Related == nil {
				t.Fatalf("line has no related array:\n%q", line)
			}
			ds = append(ds, d)
		}
		return ds
	}

	bad := write("bad \"q\".fern", "function main(): i32 {\n  let count = 1;\n  let count = 2;\n  return kount;\n}\n")

	for _, c := range []struct {
		name, env string
		args      []string
	}{
		{"flag", "", []string{"-diag-format", "json", "-color", "always", "-check", bad, h.stdlib}},
		{"environment", "FERN_DIAG_FORMAT=json", []string{"-color", "always", "-check", bad, h.stdlib}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, code := run(c.env, c.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if strings.Contains(out, "\x1b[") {
				t.Fatalf("output has colour:\n%s", out)
			}
			ds := decode(t, out)
			if len(ds) != 2 {
				t.Fatalf("%d diagnostics, want 2:\n%s", len(ds), out)
			}
			first, second := ds[0], ds[1]
			if first.Code != "E013" || first.File != bad || first.Line != 3 || first.Col != 3 || first.Severity != "error" {
				t.Errorf("first diagnostic: %+v", first)
			}
			if len(first.Related) != 1 || first.Related[0].File != bad || first.Related[0].Line != 2 || first.Related[0].Message == "" {
				t.Errorf("first diagnostic has no note at line 2: %+v", first.Related)
			}
			if second.Code != "E001" || second.Line != 4 || second.Col != 10 || second.Length != 5 || !strings.Contains(second.Message, "\"kount\"") {
				t.Errorf("second diagnostic: %+v", second)
			}
		})
	}

	t.Run("parse-error", func(t *testing.T) {
		p := write("parse.fern", "function main(): i32 {\n  return 0;\n")
		out, code := run("", "-diag-format", "json", "-check", p, h.stdlib)
		ds := decode(t, out)
		if code != 1 || len(ds) != 1 || ds[0].Code != "P001" || ds[0].File != p || ds[0].Line != 3 {
			t.Errorf("exit %d, diagnostics %+v", code, ds)
		}
	})

	t.Run("lint", func(t *testing.T) {
		p := write("lint.fern", "function main(): i32 {\n  let p = 4;\n  p = p - 1;\n  return p;\n}\n")
		out, code := run("", "-lint", "-lint-set", "modern-form=warn", "-diag-format", "json", p)
		ds := decode(t, out)
		if code != 0 || len(ds) != 1 || ds[0].Code != "modern-form" || ds[0].Severity != "warning" || ds[0].File != p || ds[0].Line != 3 {
			t.Errorf("exit %d, diagnostics %+v\n%s", code, ds, out)
		}
		out, code = run("", "-lint", "-lint-set", "modern-form=deny", "-diag-format", "json", p)
		ds = decode(t, out)
		if code != 1 || len(ds) != 1 || ds[0].Severity != "error" {
			t.Errorf("deny: exit %d, diagnostics %+v\n%s", code, ds, out)
		}
	})

	t.Run("clean-file-prints-nothing", func(t *testing.T) {
		p := write("ok.fern", "function main(): i32 {\n  return 0;\n}\n")
		if out, code := run("", "-diag-format", "json", "-check", p, h.stdlib); code != 0 || out != "" {
			t.Errorf("exit %d, stderr %q", code, out)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"-diag-format", "xml", "-check", bad}, "fern: -diag-format wants full, terse or json (got \"xml\")\n"},
			{[]string{"-q", "-diag-format", "json", "-check", bad}, "fern: -diag-format and -q do different things with the result: give one of them\n"},
		} {
			if out, code := run("", c.args...); code != 2 || out != c.want {
				t.Errorf("%v: exit %d, stderr %q, want exit 2 and %q", c.args, code, out, c.want)
			}
		}
	})

	t.Run("full-and-terse-are-unchanged", func(t *testing.T) {
		terse, _ := run("", "-q", "-check", bad, h.stdlib)
		named, _ := run("", "-diag-format", "terse", "-check", bad, h.stdlib)
		if terse != named || !strings.Contains(terse, "error[E001]") {
			t.Errorf("-diag-format terse differs from -q:\n%s\n%s", named, terse)
		}
		full, _ := run("", "-color", "never", "-check", bad, h.stdlib)
		named, _ = run("FERN_DIAG_FORMAT=json", "-diag-format", "full", "-color", "never", "-check", bad, h.stdlib)
		if full != named || !strings.Contains(full, "let count") {
			t.Errorf("-diag-format full differs from the default:\n%s\n%s", named, full)
		}
	})
}

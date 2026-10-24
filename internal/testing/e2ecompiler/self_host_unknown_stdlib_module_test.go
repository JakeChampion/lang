package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostUnknownStdlibModule(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("json2.fern", "pub function hi(): i32 { return 7; }\n")
	write("x.fern", "pub function f(): i32 { return 8; }\n")
	write("lib/x.fern", "pub function f(): i32 { return 9; }\n")
	write("mid.fern", "// mid\n  import \"core/nosuch\";\npub function f(): i32 { return 1; }\n")
	typo := write("typo.fern", "import \"std/io\";\nimport \"std/json2\";\nfunction main(): i32 { return json2.hi(); }\n")
	transitive := write("uses_mid.fern", "import \"./mid\";\nfunction main(): i32 { return mid.f(); }\n")
	reexport := write("reexport.fern", "pub use \"std/nosuch\".{a};\nfunction main(): i32 { return 0; }\n")
	subpath := write("subpath.fern", "import \"./lib/nope/x\";\nfunction main(): i32 { return x.f(); }\n")
	good := write("good.fern", "import \"std/io\";\nimport \"core/map\";\nimport \"./lib/x\";\n"+
		"function main(): i32 { let m: Map[string, i32] = map_new(2); m = m.insert(\"a\", 1); print(\"ok\"); return x.f() + m.len() - 10; }\n")

	cases := []struct {
		name  string
		args  []string
		entry string
		want  string
	}{
		{"typo-check", []string{"-check"}, typo, typo + ":2:1: error[E086]: unknown stdlib module \"std/json2\""},
		{"typo-build", []string{"-o", filepath.Join(dir, "a.out")}, typo, typo + ":2:1: error[E086]: unknown stdlib module \"std/json2\""},
		{"typo-in-imported-module", []string{"-check"}, transitive, filepath.Join(dir, "mid.fern") + ":2:3: error[E086]: unknown stdlib module \"core/nosuch\""},
		{"typo-in-reexport", []string{"-check"}, reexport, reexport + ":1:1: error[E086]: unknown stdlib module \"std/nosuch\""},
		{"missing-subpath-with-namesake", []string{"-check"}, subpath, subpath + ":1:1: error[E086]: cannot resolve import \"./lib/nope/x\": not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(h.cli, append(c.args, c.entry, h.stdlib)...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatalf("exit 0, want a failure; stderr:\n%s", stderr.String())
			}
			got, _, _ := strings.Cut(stderr.String(), "\n")
			if got != c.want {
				t.Errorf("stderr:\n%s\nwant first line:\n%s", stderr.String(), c.want)
			}
		})
	}

	t.Run("resolving-imports-load", func(t *testing.T) {
		out, err := exec.Command(h.cli, "-check", good, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("-check: %v\n%s", err, out)
		}
		tg := h.targets[0]
		bin := filepath.Join(dir, "good.out")
		if out, err := exec.Command(h.cli, "-target", tg.target, "-o", bin, good, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		run := append(append([]string{}, tg.runner...), bin)
		out, err = exec.Command(run[0], run[1:]...).CombinedOutput()
		if err != nil || string(out) != "ok\n" {
			t.Fatalf("run: %v, output %q; want exit 0 and \"ok\\n\"", err, out)
		}
	})
}

package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryExampleHasAnExpectedExit(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, ".github", "examples-expected-exits.txt"))
	if err != nil {
		t.Fatalf("read examples-expected-exits.txt: %v", err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Errorf("malformed row %q — want `name exit`", line)
			continue
		}
		listed[f[0]] = true
	}

	srcs, err := filepath.Glob(filepath.Join(root, "examples", "*.fern"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	tests, err := filepath.Glob(filepath.Join(root, "examples", "tests", "*.fern"))
	if err != nil {
		t.Fatalf("glob examples/tests: %v", err)
	}
	srcs = append(srcs, tests...)
	if len(srcs) == 0 {
		t.Fatal("no examples/*.fern found — the glob this gate shares with the workflows stopped matching")
	}
	present := map[string]bool{}
	for _, s := range srcs {
		name := strings.TrimSuffix(filepath.Base(s), ".fern")
		present[name] = true
		if !listed[name] {
			t.Errorf("examples/%s.fern has no row in .github/examples-expected-exits.txt, so the lanes do not check its exit status (#8473)", name)
		}
	}
	for name := range listed {
		if !present[name] {
			t.Errorf("%q is listed in .github/examples-expected-exits.txt but examples/%s.fern no longer exists — drop the row", name, name)
		}
	}
}

func TestExampleLanesUseTheRunner(t *testing.T) {
	for _, wf := range []string{"examples.yml", "macos.yml"} {
		path := filepath.Join("..", "..", "..", ".github", "workflows", wf)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", wf, err)
		}
		src := string(b)
		if !strings.Contains(src, "scripts/ci-run-example") {
			t.Errorf("%s does not run scripts/ci-run-example, so its examples' exit statuses are unchecked (#8473)", wf)
		}
		if strings.Contains(src, `|| echo "(exited $?)"`) {
			t.Errorf("%s still swallows an example's exit status with `|| echo \"(exited $?)\"` (#8473)", wf)
		}
	}
}

func TestRunnerRejectsACrash(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	runner := filepath.Join(root, "scripts", "ci-run-example")
	dir := t.TempDir()

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	ok := write("ok", "#!/bin/sh\nexit 208\n")
	segv := write("segv", "#!/bin/sh\nexit 139\n")
	wrong := write("wrong", "#!/bin/sh\nexit 7\n")

	cases := []struct {
		name, example, bin string
		wantFail           bool
	}{
		{"expected status passes", "factorial", ok, false},
		{"signal-range crash fails", "factorial", segv, true},
		{"wrong answer fails", "factorial", wrong, true},
		{"unlisted example fails", "not_an_example", ok, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := exec.Command(runner, c.example, c.bin).CombinedOutput()
			if c.wantFail && err == nil {
				t.Errorf("runner passed; want failure\noutput: %s", out)
			}
			if !c.wantFail && err != nil {
				t.Errorf("runner failed: %v\noutput: %s", err, out)
			}
		})
	}
}

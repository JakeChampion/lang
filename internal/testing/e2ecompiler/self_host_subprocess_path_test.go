package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The program runs args()[1] with the rest of its arguments, and prints the
// result of subprocess.
const subprocessPathProg = `function main(): i32 {
  let argv = args();
  let rest: string[] = [];
  for i in 2..argv.len() {
    rest = rest.append(argv[i]);
  }
  let r = subprocess(argv[1], rest, "");
  print(f"exit={r.exit_code}");
  print("out=" + r.stdout);
  print("err=" + r.stderr);
  return 0;
}
`

type subprocessPathCase struct {
	name string
	path *string
	args []string
	want string
}

func subprocessPathFixture(t *testing.T) (string, []subprocessPathCase) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+body+"\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/fernprobe", "in-bin", 0o755)
	write("cwd/fernprobe", "in-cwd", 0o755)
	write("cwd/fernlocal", "local", 0o755)
	write("noexec/fernprobe", "no-exec", 0o644)
	bin, noexec, missing := filepath.Join(root, "bin"), filepath.Join(root, "noexec"), filepath.Join(root, "missing")
	str := func(s string) *string { return &s }
	return root, []subprocessPathCase{
		{"bare name uses PATH, not the current directory", str(bin + ":/usr/bin:/bin"), []string{"fernprobe"}, "exit=0\nout=in-bin\n\nerr=\n"},
		{"an empty element is the current directory", str(missing + "::/bin"), []string{"fernlocal"}, "exit=0\nout=local\n\nerr=\n"},
		{"a name with a slash does not use PATH", str(bin), []string{"./fernprobe"}, "exit=0\nout=in-cwd\n\nerr=\n"},
		{"a missing tool", str(bin), []string{"ferntoolmissing"}, "exit=127\nout=\nerr=ferntoolmissing: No such file or directory\n\n"},
		{"a later directory after EACCES", str(noexec + ":" + bin), []string{"fernprobe"}, "exit=0\nout=in-bin\n\nerr=\n"},
		{"EACCES when no directory runs it", str(noexec), []string{"fernprobe"}, "exit=127\nout=\nerr=fernprobe: Permission denied\n\n"},
		{"no PATH searches /bin and /usr/bin", nil, []string{"sh", "-c", "echo fallback"}, "exit=0\nout=fallback\n\nerr=\n"},
	}
}

func checkSubprocessPath(t *testing.T, run func(args ...string) *exec.Cmd) {
	t.Helper()
	root, cases := subprocessPathFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := run(tc.args...)
			cmd.Dir = filepath.Join(root, "cwd")
			var env []string
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "PATH=") {
					env = append(env, kv)
				}
			}
			if tc.path != nil {
				env = append(env, "PATH="+*tc.path)
			}
			cmd.Env = env
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, &errb)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelfHostSubprocessPath(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "subprocess_path.fern")
	if err := os.WriteFile(src, []byte(subprocessPathProg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		checkSubprocessPath(t, func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		bin := cli.arm64Binary(t, src)
		checkSubprocessPath(t, func(args ...string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, bin, args...) })
	})
}

func TestSelfHostArm64DarwinSubprocessPath(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := filepath.Join(t.TempDir(), "subprocess_path.fern")
	if err := os.WriteFile(src, []byte(subprocessPathProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	checkSubprocessPath(t, func(args ...string) *exec.Cmd { return exec.Command(bin, args...) })
}

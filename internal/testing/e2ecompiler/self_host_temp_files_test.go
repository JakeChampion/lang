package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The program creates three temporary directories and two temporary
// files, prints their paths, and removes nothing, so the test can read
// their modes before it cleans up.
const tempNamesSrc = `import "std/io";
function main(): i32 {
  let i: i32 = 0;
  while (i < 3) {
    match (temp_dir("sec")) {
      Ok(d) => { print("dir " + d); },
      Err(_) => { print("dir err"); }
    }
    i = i + 1;
  }
  i = 0;
  while (i < 2) {
    let made: Result[io.TempFile, IoError] = io.temp_file("sec");
    match (made) {
      Ok(t) => {
        print("file " + t.path);
        match (t.file.write("secret")) { Ok(_) => { }, Err(_) => { print("write err"); } }
        match (t.file.close()) { Ok(_) => { }, Err(_) => { print("close err"); } }
      },
      Err(_) => { print("file err"); }
    }
    i = i + 1;
  }
  match (io.temp_file("a/b")) {
    Ok(t) => { print("slash accepted " + t.path); },
    Err(e) => { match (e) { Other(_, m, n) => { print("slash refused " + n.to_string()); }, _ => { print("slash refused other"); } } }
  }
  return 0;
}
`

// The directory holds a link to a file, a dangling link, and the file.
// An exclusive open must refuse both links, a no-follow read must refuse
// the link to the file, and a plain read follows it, which is the control.
const symlinkSrc = `function kind(own r: Result[Reader, IoError]): string {
  match (r) {
    Ok(reader) => { match (reader.close()) { Ok(_) => { }, Err(_) => { } } return "ok"; },
    Err(e) => {
      match (e) {
        AlreadyExists(_) => { return "exists"; },
        NotFound(_) => { return "notfound"; },
        Other(_, _, n) => { return "errno " + n.to_string(); },
        _ => { return "other"; }
      }
    }
  }
}
function wkind(own r: Result[Writer, IoError]): string {
  match (r) {
    Ok(w) => { match (w.close()) { Ok(_) => { }, Err(_) => { } } return "ok"; },
    Err(e) => {
      match (e) {
        AlreadyExists(_) => { return "exists"; },
        NotFound(_) => { return "notfound"; },
        Other(_, _, n) => { return "errno " + n.to_string(); },
        _ => { return "other"; }
      }
    }
  }
}
function main(): i32 {
  let d: string = args()[1];
  print("exclusive link " + wkind(open_exclusive(d + "/link")));
  print("exclusive dangling " + wkind(open_exclusive(d + "/dangle")));
  print("nofollow link " + kind(open_reader_with(d + "/link", 512)));
  print("follow link " + kind(open_reader(d + "/link")));
  print("nofollow file " + kind(open_reader_with(d + "/target", 512)));
  return 0;
}
`

// The program keeps a file open and asks a child shell to list its own
// descriptors. The file must not be among them.
const cloexecSrc = `function main(): i32 {
  let path: string = args()[1];
  match (open_reader(path)) {
    Ok(r) => {
      let child: ProcessResult = subprocess("/bin/sh", ["-c", "ls -l /proc/self/fd"], "");
      print(child.stdout);
      print("child exit " + child.exit_code.to_string());
      match (r.close()) { Ok(_) => { }, Err(_) => { } }
    },
    Err(_) => { print("open err"); }
  }
  return 0;
}
`

var tempNameRe = regexp.MustCompile(`^/tmp/sec-[0-9a-f]{16}$`)

func TestSelfHostTempFiles(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	build := func(t *testing.T, target, name, src string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, name)
		args := []string{"-target", target, "-o", bin, path, cli.stdlib}
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
		}
		return bin
	}
	run := func(t *testing.T, target, bin string, args ...string) string {
		t.Helper()
		var c *exec.Cmd
		if target == "arm64-linux" {
			c = runArm64Bin(qemu, bin, args...)
		} else {
			c = runX86_64Bin(cli.runner, bin, args...)
		}
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v\nstdout: %s\nstderr: %s", bin, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			t.Run("names", func(t *testing.T) {
				out := run(t, target, build(t, target, "names", tempNamesSrc))
				seen := map[string]bool{}
				dirs, files := 0, 0
				for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
					kind, path, _ := strings.Cut(line, " ")
					switch kind {
					case "dir", "file":
						defer os.RemoveAll(path)
						if !tempNameRe.MatchString(path) {
							t.Errorf("%s name %q does not match %s", kind, path, tempNameRe)
						}
						if seen[path] {
							t.Errorf("%s name %q repeats", kind, path)
						}
						seen[path] = true
						info, err := os.Lstat(path)
						if err != nil {
							t.Errorf("%s %q: %v", kind, path, err)
							continue
						}
						if kind == "dir" {
							dirs++
							if !info.IsDir() || info.Mode().Perm() != 0o700 {
								t.Errorf("dir %q: mode %v, want a directory with 0700", path, info.Mode())
							}
						} else {
							files++
							if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 6 {
								t.Errorf("file %q: mode %v size %d, want a regular 0600 file of 6 bytes", path, info.Mode(), info.Size())
							}
						}
					case "slash":
						if line != "slash refused 22" {
							t.Errorf("a prefix with a separator: %q, want \"slash refused 22\"", line)
						}
					default:
						t.Errorf("unexpected line %q", line)
					}
				}
				if dirs != 3 || files != 2 {
					t.Errorf("%d directories and %d files, want 3 and 2:\n%s", dirs, files, out)
				}
			})
			t.Run("symlinks", func(t *testing.T) {
				d := t.TempDir()
				if err := os.WriteFile(filepath.Join(d, "target"), []byte("content"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(d, "target"), filepath.Join(d, "link")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(d, "nowhere"), filepath.Join(d, "dangle")); err != nil {
					t.Fatal(err)
				}
				out := run(t, target, build(t, target, "symlinks", symlinkSrc), d)
				want := "exclusive link exists\nexclusive dangling exists\nnofollow link errno 40\nfollow link ok\nnofollow file ok\n"
				if out != want {
					t.Errorf("symlink probes printed:\n%swant:\n%s", out, want)
				}
				if _, err := os.Lstat(filepath.Join(d, "nowhere")); err == nil {
					t.Errorf("the exclusive open through the dangling link created its target")
				}
			})
			t.Run("close-on-exec", func(t *testing.T) {
				if target == "arm64-linux" {
					t.Skip("the child is a host shell; QEMU user mode keeps descriptors of its own open in it")
				}
				d := t.TempDir()
				secret := filepath.Join(d, "held-open.txt")
				if err := os.WriteFile(secret, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				out := run(t, target, build(t, target, "cloexec", cloexecSrc), secret)
				if !strings.Contains(out, "child exit 0") || !strings.Contains(out, "/proc/self/fd") && !strings.Contains(out, "->") {
					t.Fatalf("the child did not list its descriptors:\n%s", out)
				}
				if strings.Contains(out, secret) {
					t.Errorf("the child inherited the open file:\n%s", out)
				}
			})
		})
	}
}

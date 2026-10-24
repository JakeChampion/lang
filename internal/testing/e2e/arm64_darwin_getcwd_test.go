package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestArm64DarwinGetcwdValue(t *testing.T) {
	const prog = `function main(): i32 {
  let a: string[] = args();
  if (a.len() < 2) { return 97; }
  let p: string = a[1];
  let cwd: string = "";
  match (getcwd()) {
    Ok(c) => { cwd = c; },
    Err(e) => { return 98; }
  }
  print(cwd);
  print(p);
  return 0;
}
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	const operand = "newroot"
	cmd := exec.Command(out, operand)
	cmd.Dir = dir
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v (97 = no argv[1], 98 = getcwd answered Err)", err)
	}
	if string(got) != want+"\n"+operand+"\n" {
		t.Errorf("getcwd() and the caller's operand did not both survive\n got: %q\nwant: %q",
			got, want+"\n"+operand+"\n")
	}
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scan lends two arrays to the mismatch kernel, so it takes the array kernel
// in place. scan_view reads views that may be strings, so it keeps the
// wrapper that tests the layout. cmp and cmp_view are leaves: the kernel
// call reads its arguments and returns an index, so both splice into run.
const lentKernelProgram = `@noinline function scan(a: u8[], ao: i32, b: u8[], bo: i32, n: i32): i32 {
  return __mismatch_bytes(a, ao, b, bo, n);
}
@noinline function scan_view(a: [u8], ao: i32, b: [u8], bo: i32, n: i32): i32 {
  return __mismatch_bytes(a, ao, b, bo, n);
}
function cmp(a: u8[], b: u8[]): i32 {
  let n: i32 = a.len();
  if (b.len() < n) { n = b.len(); }
  let i: i32 = __mismatch_bytes(a, 0, b, 0, n);
  if (i < n) { return a[i] as i32 - b[i] as i32; }
  return a.len() - b.len();
}
function cmp_view(a: [u8], b: [u8]): i32 {
  let n: i32 = a.len();
  if (b.len() < n) { n = b.len(); }
  let i: i32 = __mismatch_bytes(a, 0, b, 0, n);
  if (i < n) { return a[i] as i32 - b[i] as i32; }
  return a.len() - b.len();
}
@noinline function run(a: u8[], b: u8[]): i32 {
  return cmp(a, b) * 2 + cmp(b, a) + __sum_bytes_array(a) * 100;
}
@noinline function run_view(a: u8[], b: u8[]): i32 {
  return cmp_view(a, b) * 2 + cmp_view(b, a);
}
function main(): i32 {
  let a: u8[] = [1u8, 2u8, 3u8, 4u8];
  let b: u8[] = [1u8, 2u8, 5u8];
  let c: u8[] = [1u8, 2u8, 3u8];
  if (scan(a, 0, b, 0, 3) != 2 || scan_view(a, 1, b, 1, 2) != 1) { return 1; }
  if (run(a, b) != 998 || run(a, c) != 1001) { return 2; }
  if (run_view(a, b) != -2 || run_view(a, c) != 1) { return 3; }
  print("ok");
  return 0;
}
`

func TestSelfHostLentArrayKernel(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "lent.fern")
	if err := os.WriteFile(src, []byte(lentKernelProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	call := map[string]string{"x86-64-linux": "call __fn_", "arm64-linux": "bl __fn_"}
	wrapper := "__fn___sem_byte_view_mismatch"
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		listing := string(asm)
		if body := condBranchBody(t, listing, "scan"); strings.Contains(body, wrapper) {
			t.Errorf("%s: scan calls the layout wrapper for two lent arrays:\n%s", target, body)
		}
		if body := condBranchBody(t, listing, "scan_view"); !strings.Contains(body, wrapper) {
			t.Errorf("%s: scan_view lost the layout wrapper its views need:\n%s", target, body)
		}
		if body := condBranchBody(t, listing, "run"); strings.Contains(body, call[target]+"cmp") {
			t.Errorf("%s: run still calls cmp:\n%s", target, body)
		}
		if body := condBranchBody(t, listing, "run_view"); strings.Contains(body, call[target]+"cmp_view") {
			t.Errorf("%s: run_view still calls cmp_view:\n%s", target, body)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		out, err := run.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ok" {
			t.Errorf("%s: %v\n%s", tg.target, err, out)
		}
	}
}

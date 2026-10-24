package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLetElseFullInitializer(t *testing.T) {
	const source = `
struct Box { n: i32 }
function (b: Box) get(): Option[i32] { return Some(b.n); }
function early(flag: boolean): i32 {
  let Some(n) = { if (flag) { return 17; } Some(3) } else { return 18; };
  return n;
}
function absent(): i32 {
  let missing: Option[i32] = None;
  let Some(n) = { missing } else { return 19; };
  return n;
}
function if_return(flag: boolean): i32 {
  let n: i32 = if (flag) { return 23; } else { 4 };
  return n + 30;
}
function match_return(flag: boolean): i32 {
  let input: Option[i32] = if (flag) { Some(5) } else { None };
  let n: i32 = match (input) { Some(value) => { return value + 20; }, None => 4 };
  return n + 30;
}
function closure_scope(): i32 {
  let n: i32 = { let f: () => i32 = (): i32 => { return 31; }; f() };
  return n + 1;
}
function call_zero(f: () => i32): i32 { return f(); }
function captured_scope(base: i32): i32 {
  let direct: i32 = { let f: () => i32 = (): i32 => { return base + 1; }; f() };
  let escaped: i32 = { let f: () => i32 = (): i32 => { return base + 2; }; call_zero(f) };
  return direct + escaped;
}
function propagated(flag: boolean): Result[i32, string] {
  let n: i32 = { let source: Result[i32, string] = if (flag) { Err("failed") } else { Ok(6) }; source? };
  return Ok(n + 1);
}
function only_exit(flag: boolean): string {
  loop {
    let value: string = { if (flag) { return "done"; } break; };
    return value;
  }
  return "break";
}
function main(): i32 {
  let Some(value) = { Some(7) } else { return 1; };
  let 3 = { 3 } else { return 2; };
  let Some(field) = Box { n: 11 }.get() else { return 3; };
  if (value != 7 || field != 11 || early(false) != 3 || early(true) != 17 || absent() != 19) { return 4; }
  let sum: i32 = 0;
  for n in [1, 2, 3] { sum = sum + n; }
  if let Some(n) = Some(4) { sum = sum + n; }
  if (sum != 10) { return 5; }
  if (if_return(true) != 23 || if_return(false) != 34 || match_return(true) != 25 || match_return(false) != 34 || closure_scope() != 32) { return 6; }
  let total: i32 = 0;
  for i in [0, 1, 2, 3, 4] {
    let Some(n) = { if (i == 2) { continue; } if (i == 4) { break; } Some(i) } else { return 7; };
    total = total + n;
  }
  if (total != 4) { return 8; }
  let outer: i32 = 0;
  let n: i32 = { outer = 5; 7 };
  if (outer != 5 || n != 7) { return 9; }
  if (captured_scope(10) != 23) { return 10; }
  if (!match (propagated(false)) { Ok(n) => n == 7, Err(_) => false }) { return 11; }
  if (!match (propagated(true)) { Err(message) => message == "failed", Ok(_) => false }) { return 12; }
  if (only_exit(true) != "done" || only_exit(false) != "break") { return 13; }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string]*exec.Cmd{
		"Go-check":    exec.Command(oracle, "-check", path),
		"Go-interp":   exec.Command(oracle, "-interp", path),
		"self-check":  runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
		"self-interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("initializer: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("initializer: exit %d\n%s", code, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
				t.Fatalf("allocation census: %v\n%s", err, stderr)
			}
		})
	}
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func copySelfHostTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = "../../../compiler"
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".fern") {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy compiler tree: %v", err)
	}
	return dir
}

const treeshakeHeavyProg = `import "core/cmp";
import "std/json";
import "std/http" as http;
import "std/regex" as regex;
import "std/time" as time;
@derive(cmp.Eq, cmp.Ord, cmp.Hash, json.Json)
struct Rec { name: string, id: i32, tag: string }
function main(): i32 {
    let a = Rec { name: "x", id: 1, tag: "p" };
    let b = Rec { name: "y", id: 2, tag: "q" };
    let n = 0;
    if (a.eq(a)) { n = n + 1; }
    if (a.cmp(b) < 0) { n = n + 1; }
    if (a.hash() != b.hash()) { n = n + 1; }
    if (a.to_json().len() > 0) { n = n + 1; }
    if (http.http_status_text(200).len() > 0) { n = n + 1; }
    if ({ let __checked_regex_0: regex.Regex = match (regex.compile("a")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("a") }) { n = n + 1; }
    let d = time.date_make(2026, 6, 28);
    if (d.year == 2026) { n = n + 1; }
    return n;
}`

const treeshakeLightProg = `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { let a = P { x: 1, y: 2 }; let b = P { x: 1, y: 2 }; let c = P { x: 1, y: 9 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`

func TestSelfHostTreeshakeStdlibIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	runProg := func(name, src string, want int) {
		entry := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(entry, []byte(src+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		if _, code := runFixtureInterp(t, entry, ""); code != want {
			t.Fatalf("%s native interp = %d, want %d", name, code, want)
		}
		if out, _ := runDriver(entry, root, "-treeshake", "-decide"); strings.TrimSpace(out) != "ir" {
			t.Errorf("%s -treeshake decide = %q, want \"ir\"", name, strings.TrimSpace(out))
		}
		asm, _ := runDriver(entry, root, "-treeshake")
		if len(asm) == 0 {
			t.Fatalf("%s: driver emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name+"_bin", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s -treeshake run = %d, want %d", name, code, want)
		}
	}

	t.Run("light-derive-eq", func(t *testing.T) { runProg("ts_light", treeshakeLightProg, 42) })

	t.Run("heavy", func(t *testing.T) {
		entry := filepath.Join(dir, "ts_heavy.fern")
		if err := os.WriteFile(entry, []byte(treeshakeHeavyProg+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		if _, code := runFixtureInterp(t, entry, ""); code != 7 {
			t.Fatalf("heavy native interp = %d, want 7", code)
		}
		if out, _ := runDriver(entry, root, "-no-treeshake", "-decide"); strings.TrimSpace(out) != "ir" {
			t.Errorf("heavy decide (no treeshake) = %q, want \"ir\" — the merged bundle is being refused again", strings.TrimSpace(out))
		}
		if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
			t.Errorf("heavy decide (default treeshake) = %q, want \"ir\"", strings.TrimSpace(out))
		}
		asm, _ := runDriver(entry, root)
		if len(asm) == 0 {
			t.Fatal("heavy (pruned): 0 bytes")
		}
		bin := buildBin(t, gcc, dir, "ts_heavy_ir", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("heavy (pruned) run = %d, want 7", code)
		}
		if noPrune, _ := runDriver(entry, root, "-no-treeshake"); len(noPrune) == 0 {
			t.Error("heavy (unpruned) emitted 0 bytes, want a build — the merged bundle is being refused again")
		}
	})

	t.Run("guard-reachability", func(t *testing.T) {
		const guardProg = `function guard_only(n: i32): boolean { return n > 5; }
enum E { N(i32), Z }
function main(): i32 {
    let e: E = E.N(7);
    match (e) { E.N(v) when guard_only(v) => { return 42; }, _ => { return 0; } }
}`
		entry := filepath.Join(dir, "ts_guard.fern")
		if err := os.WriteFile(entry, []byte(guardProg+"\n"), 0o644); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		if _, code := runFixtureInterp(t, entry, ""); code != 42 {
			t.Fatalf("guard native interp = %d, want 42", code)
		}
		asm, _ := runDriver(entry, root, "-treeshake")
		if len(asm) == 0 {
			t.Fatalf("guard: driver emitted 0 bytes")
		}
		bin := buildBin(t, gcc, dir, "ts_guard_bin", asm)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("guard -treeshake run = %d, want 42 (guard-only fn pruned by treeshake?)", code)
		}
	})
}

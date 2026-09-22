package e2eharness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// WasiPollStorageProbe keeps the caller's array alive across repeated polls.
// The host fixture supplies borrowed pollable handles and ready-index lists.
func WasiPollStorageProbe(expr string) string {
	return `function main(): i32 {
    var ps: i32[] = [41, 42];
    var i: i32 = 0;
    var stable: i64 = 0;
    var result: i32 = 0;
    while (i < 32) {
        result = ` + expr + `;
        var used: i64 = __heap_bump_bytes();
        if (i == 0) { stable = used; }
        if (used != stable) { return -1000; }
        i = i + 1;
    }
    return result;
}`
}

// WasiPollCensusProbe uses a real ready timer, with every resource released.
func WasiPollCensusProbe(expr string) string {
	return `function main(): i32 {
    var i: i32 = 0;
    while (i < 32) {
        var duration: i64 = 0;
        var p: i32 = wasm_timer_pollable(duration);
        var ps: i32[] = [p];
        var result: i32 = ` + expr + `;
        wasm_pollable_drop(p);
        if (result != 0) { return 1; }
        i = i + 1;
    }
    return 0;
}`
}

// CheckWasiPollStorage substitutes only host imports. Guest polling, canonical
// allocation, and reclamation remain production code. It checks returned list
// ordering, empty results, and ownership of the optional finite-wait timer.
func CheckWasiPollStorage(t *testing.T, modulePath string, finite bool) {
	t.Helper()
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	out, err := exec.Command("wasm-tools", "print", modulePath).CombinedOutput()
	if err != nil {
		t.Fatalf("print poll module: %v\n%s", err, out)
	}
	wat := string(out)
	main := regexp.MustCompile(`\(export "main" \(func ([^\s)]+)\)\)`).FindStringSubmatch(wat)
	if len(main) != 2 {
		main = regexp.MustCompile(`\(func (\$main)\s`).FindStringSubmatch(wat)
	}
	alloc := regexp.MustCompile(`\(export "cabi_realloc" \(func ([^\s)]+)\)\)`).FindStringSubmatch(wat)
	if len(main) != 2 || len(alloc) != 2 {
		t.Fatal("poll module lacks main or canonical allocator")
	}
	imports := regexp.MustCompile(`\(import "([^"]+)" "([^"]+)" \(func ((?:\$[^\s()]+\s+)?\(;[0-9]+;\)) \(type ([0-9]+)\)\)\)`)
	seen := 0
	wat = imports.ReplaceAllStringFunc(wat, func(decl string) string {
		m := imports.FindStringSubmatch(decl)
		body := "unreachable"
		switch {
		case strings.HasPrefix(m[1], "wasi:io/poll@") && m[2] == "poll":
			seen++
			body = `(local $list i32) (local $count i32) (local $first i32)
    (if (i32.ne (local.get 1) (i32.add (i32.const 2) (global.get $ph_finite))) (then unreachable))
    (if (i32.ne (i32.load (local.get 0)) (i32.const 41)) (then unreachable))
    (if (i32.ne (i32.load offset=4 (local.get 0)) (i32.const 42)) (then unreachable))
    (if (global.get $ph_finite) (then
      (if (i32.or (i32.eqz (global.get $ph_owned))
        (i32.ne (i32.load offset=8 (local.get 0)) (global.get $ph_handle))) (then unreachable))))
    (local.set $count (select (i32.const 2) (i32.const 1) (i32.eq (global.get $ph_mode) (i32.const 3))))
    (if (i32.eqz (global.get $ph_mode)) (then (local.set $count (i32.const 0))))
    (local.set $first (select (i32.const 0) (i32.const 1) (i32.eq (global.get $ph_mode) (i32.const 1))))
    (if (i32.eq (global.get $ph_mode) (i32.const 4)) (then
      (local.set $first (i32.add (i32.const 1) (global.get $ph_finite)))))
    (if (local.get $count) (then
      (local.set $list (call $ph_alloc (i32.mul (local.get $count) (i32.const 4))))
      (i32.store (local.get $list) (local.get $first))
      (if (i32.eq (local.get $count) (i32.const 2)) (then (i32.store offset=4 (local.get $list) (i32.const 0))))))
    (i32.store (local.get 2) (local.get $list))
    (i32.store offset=4 (local.get 2) (local.get $count))`
		case strings.HasPrefix(m[1], "wasi:clocks/monotonic-clock@") && m[2] == "subscribe-duration":
			body = `(if (global.get $ph_owned) (then unreachable))
    (global.set $ph_owned (i32.const 1)) (global.get $ph_handle)`
		case strings.HasSuffix(m[2], "[resource-drop]pollable"):
			body = `(if (i32.or (i32.eqz (global.get $ph_owned)) (i32.ne (local.get 0) (global.get $ph_handle))) (then unreachable))
    (global.set $ph_owned (i32.const 0))`
		}
		return "(func " + m[3] + " (type " + m[4] + ") " + body + ")"
	})
	if seen != 1 || strings.Contains(wat, "(import ") {
		t.Fatal("poll fixture did not replace exactly one poll import")
	}
	finiteValue := 0
	if finite {
		finiteValue = 1
	}
	probe := fmt.Sprintf(`
  (global $ph_mode (mut i32) (i32.const 0))
  (global $ph_handle (mut i32) (i32.const 0))
  (global $ph_owned (mut i32) (i32.const 0))
  (global $ph_finite i32 (i32.const %d))
  (func $ph_alloc (param $n i32) (result i32)
    (call %s (i32.const 0) (i32.const 0) (i32.const 4) (local.get $n)))
  (func (export "poll_probe") (param $mode i32) (param $handle i32) (result i32)
    (local $result i32)
    (global.set $ph_mode (local.get $mode))
    (global.set $ph_handle (local.get $handle))
    (local.set $result (call %s))
    (if (global.get $ph_owned) (then unreachable))
    (local.get $result))
`, finiteValue, alloc[1], main[1])
	end := strings.LastIndex(wat, ")")
	path := filepath.Join(t.TempDir(), "poll-host.wat")
	if err := os.WriteFile(path, []byte(wat[:end]+probe+")"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []int{0, 7} {
		for mode, want := range []int{-1, 0, 1, 1, 1} {
			if finite && mode == 4 {
				want = -1
			}
			t.Run(fmt.Sprintf("handle%d/mode%d", handle, mode), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "wasmtime", "run", "--invoke", "poll_probe", path, strconv.Itoa(mode), strconv.Itoa(handle))
				got, err := cmd.Output()
				if err != nil {
					if ee, ok := err.(*exec.ExitError); ok {
						t.Fatalf("poll probe: %v\n%s", err, ee.Stderr)
					}
					t.Fatal(err)
				}
				if strings.TrimSpace(string(got)) != strconv.Itoa(want) {
					t.Fatalf("poll result %s, want %d (-1000 means retained storage)", got, want)
				}
			})
		}
	}
}

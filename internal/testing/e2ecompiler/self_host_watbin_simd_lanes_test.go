package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWatbinSimdLanes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping watbin SIMD lane test")
	}
	wasmTools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping watbin SIMD lane test")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	asmWatPath := buildWatbinAssembler(t, gcc, runner, driverBin, dir)

	watPath := filepath.Join(dir, "target.wat")
	if err := os.WriteFile(watPath, []byte(simdLanesWat), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(path string) (int, string) {
		cmd := exec.Command(wasmtime, "run", "--dir", dir, path)
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	if code, out := run(watPath); code != 0 {
		t.Fatalf("the WAT leg exits %d, so the module is not the oracle it is meant to be\n%s", code, out)
	}
	wasmPath := assembleWithWatbin(t, wasmtime, asmWatPath, dir, "simd_lanes")
	if out, err := exec.Command(wasmTools, "validate", "--features", "simd", wasmPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate rejects watbin's module: %v\n%s", err, out)
	}
	if code, out := run(wasmPath); code != 0 {
		t.Errorf("watbin's module exits %d, want 0: check %d failed\n%s", code, code, out)
	}

	if err := os.WriteFile(watPath, []byte(simdLaneOutOfRangeWat), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).CombinedOutput()
	if err == nil {
		t.Fatalf("watbin assembled f64x2.extract_lane 2, want a refusal\n%s", out)
	}
	if !strings.Contains(string(out), "lane index out of range: f64x2.extract_lane 2") {
		t.Errorf("watbin refused the module for another reason\n%s", out)
	}
}

const simdLaneOutOfRangeWat = `(module
  (memory (export "memory") 1)
  (func (export "_start") (result f64)
    f64.const 1.0
    f64x2.splat
    f64x2.extract_lane 2)
)
`

const simdLanesWat = `(module
  (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
  (memory (export "memory") 1)
  (func $f64_eq (param $x f64) (param $y f64) (result i32) (f64.eq (local.get $x) (local.get $y)))
  (func $fail (param $code i32) (call $exit (local.get $code)))
  (func $flat_checks
    (local $a v128) (local $b v128)
    f64.const 3.0
    f64x2.splat
    f64.const -2.5
    f64x2.replace_lane 1
    local.set $a
    f64.const 1.5
    f64x2.splat
    f64.const 4.0
    f64x2.replace_lane 1
    local.set $b
    local.get $a
    local.get $b
    f64x2.add
    f64x2.extract_lane 0
    f64.const 4.5
    f64.eq
    i32.eqz
    if
      i32.const 34
      call $fail
    end
    local.get $a
    local.get $b
    f64x2.lt
    i64x2.extract_lane 1
    i64.const -1
    i64.ne
    if
      i32.const 34
      call $fail
    end)
  (func $main
    (local $a v128) (local $b v128) (local $m v128) (local $c v128) (local $s v128)
    (local.set $a (f64x2.replace_lane 1 (f64x2.splat (f64.const 3.0)) (f64.const -2.5)))
    (local.set $b (f64x2.replace_lane 1 (f64x2.splat (f64.const 1.5)) (f64.const 4.0)))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.add (local.get $a) (local.get $b))) (f64.const 4.5))) (then (call $fail (i32.const 1))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.add (local.get $a) (local.get $b))) (f64.const 1.5))) (then (call $fail (i32.const 1))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.sub (local.get $a) (local.get $b))) (f64.const -6.5))) (then (call $fail (i32.const 2))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.mul (local.get $a) (local.get $b))) (f64.const -10.0))) (then (call $fail (i32.const 3))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.div (local.get $a) (local.get $b))) (f64.const 2.0))) (then (call $fail (i32.const 4))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.min (local.get $a) (local.get $b))) (f64.const 1.5))) (then (call $fail (i32.const 5))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.max (local.get $a) (local.get $b))) (f64.const 4.0))) (then (call $fail (i32.const 6))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.pmin (local.get $a) (local.get $b))) (f64.const -2.5))) (then (call $fail (i32.const 7))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.pmax (local.get $a) (local.get $b))) (f64.const 3.0))) (then (call $fail (i32.const 8))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.abs (local.get $a))) (f64.const 2.5))) (then (call $fail (i32.const 9))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.neg (local.get $a))) (f64.const -3.0))) (then (call $fail (i32.const 10))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.sqrt (local.get $b))) (f64.const 2.0))) (then (call $fail (i32.const 11))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.floor (local.get $a))) (f64.const -3.0))) (then (call $fail (i32.const 12))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.ceil (local.get $b))) (f64.const 2.0))) (then (call $fail (i32.const 13))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.trunc (local.get $a))) (f64.const -2.0))) (then (call $fail (i32.const 14))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (f64x2.nearest (local.get $b))) (f64.const 2.0))) (then (call $fail (i32.const 15))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.nearest (local.get $a))) (f64.const -2.0))) (then (call $fail (i32.const 15))))
    (local.set $m (f64x2.lt (local.get $a) (local.get $b)))
    (if (i64.ne (i64x2.extract_lane 1 (local.get $m)) (i64.const -1)) (then (call $fail (i32.const 16))))
    (if (i64.ne (i64x2.extract_lane 0 (local.get $m)) (i64.const 0)) (then (call $fail (i32.const 16))))
    (if (i64.ne (i64x2.extract_lane 0 (f64x2.gt (local.get $a) (local.get $b))) (i64.const -1)) (then (call $fail (i32.const 17))))
    (if (i64.ne (i64x2.extract_lane 0 (f64x2.le (local.get $a) (local.get $b))) (i64.const 0)) (then (call $fail (i32.const 18))))
    (if (i64.ne (i64x2.extract_lane 1 (f64x2.ge (local.get $a) (local.get $b))) (i64.const 0)) (then (call $fail (i32.const 19))))
    (if (i64.ne (i64x2.extract_lane 0 (f64x2.eq (local.get $a) (local.get $a))) (i64.const -1)) (then (call $fail (i32.const 20))))
    (if (i64.ne (i64x2.extract_lane 0 (f64x2.ne (local.get $a) (local.get $a))) (i64.const 0)) (then (call $fail (i32.const 21))))
    (local.set $s (v128.bitselect (local.get $a) (local.get $b) (local.get $m)))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 0 (local.get $s)) (f64.const 1.5))) (then (call $fail (i32.const 22))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (local.get $s)) (f64.const -2.5))) (then (call $fail (i32.const 22))))
    (if (i64.ne (i64x2.extract_lane 1 (v128.and (local.get $m) (local.get $m))) (i64.const -1)) (then (call $fail (i32.const 23))))
    (if (i64.ne (i64x2.extract_lane 0 (v128.or (local.get $m) (v128.not (local.get $m)))) (i64.const -1)) (then (call $fail (i32.const 23))))
    (if (i64.ne (i64x2.extract_lane 1 (v128.xor (local.get $m) (local.get $m))) (i64.const 0)) (then (call $fail (i32.const 23))))
    (if (i64.ne (i64x2.extract_lane 1 (v128.andnot (local.get $m) (local.get $m))) (i64.const 0)) (then (call $fail (i32.const 23))))
    (v128.store (i32.const 64) (local.get $a))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (v128.load (i32.const 64))) (f64.const -2.5))) (then (call $fail (i32.const 24))))
    (local.set $c (f32x4.replace_lane 3 (f32x4.splat (f32.demote_f64 (f64.const 1.5))) (f32.demote_f64 (f64.const -4.0))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.add (local.get $c) (local.get $c)))) (f64.const -8.0))) (then (call $fail (i32.const 25))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.mul (local.get $c) (local.get $c)))) (f64.const 2.25))) (then (call $fail (i32.const 26))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.sub (local.get $c) (local.get $c)))) (f64.const 0.0))) (then (call $fail (i32.const 27))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.div (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 2.0)))))) (f64.const -2.0))) (then (call $fail (i32.const 28))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.min (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0)))))) (f64.const -4.0))) (then (call $fail (i32.const 29))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.max (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0)))))) (f64.const 0.0))) (then (call $fail (i32.const 29))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.pmin (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0)))))) (f64.const 0.0))) (then (call $fail (i32.const 29))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.pmax (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0)))))) (f64.const 1.5))) (then (call $fail (i32.const 29))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 3 (f32x4.abs (local.get $c)))) (f64.const 4.0))) (then (call $fail (i32.const 30))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.neg (local.get $c)))) (f64.const -1.5))) (then (call $fail (i32.const 30))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.sqrt (f32x4.splat (f32.demote_f64 (f64.const 9.0)))))) (f64.const 3.0))) (then (call $fail (i32.const 30))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.floor (local.get $c)))) (f64.const 1.0))) (then (call $fail (i32.const 31))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.ceil (local.get $c)))) (f64.const 2.0))) (then (call $fail (i32.const 31))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.trunc (local.get $c)))) (f64.const 1.0))) (then (call $fail (i32.const 31))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 0 (f32x4.nearest (local.get $c)))) (f64.const 2.0))) (then (call $fail (i32.const 31))))
    (if (i32.ne (i32x4.extract_lane 3 (f32x4.lt (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0))))) (i32.const -1)) (then (call $fail (i32.const 32))))
    (if (i32.ne (i32x4.extract_lane 3 (f32x4.gt (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0))))) (i32.const 0)) (then (call $fail (i32.const 32))))
    (if (i32.ne (i32x4.extract_lane 3 (f32x4.le (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0))))) (i32.const -1)) (then (call $fail (i32.const 32))))
    (if (i32.ne (i32x4.extract_lane 3 (f32x4.ge (local.get $c) (f32x4.splat (f32.demote_f64 (f64.const 0.0))))) (i32.const 0)) (then (call $fail (i32.const 32))))
    (if (i32.ne (i32x4.extract_lane 0 (f32x4.eq (local.get $c) (local.get $c))) (i32.const -1)) (then (call $fail (i32.const 32))))
    (if (i32.ne (i32x4.extract_lane 0 (f32x4.ne (local.get $c) (local.get $c))) (i32.const 0)) (then (call $fail (i32.const 32))))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (f64x2.promote_low_f32x4 (local.get $c))) (f64.const 1.5))) (then (call $fail (i32.const 33))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 1 (f32x4.demote_f64x2_zero (local.get $a)))) (f64.const -2.5))) (then (call $fail (i32.const 33))))
    (if (i32.eqz (call $f64_eq (f64.promote_f32 (f32x4.extract_lane 2 (f32x4.demote_f64x2_zero (local.get $a)))) (f64.const 0.0))) (then (call $fail (i32.const 33))))
    (call $flat_checks)
    (call $exit (i32.const 0)))
  (export "_start" (func $main)))
`

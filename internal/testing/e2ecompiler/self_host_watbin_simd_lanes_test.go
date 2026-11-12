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
	for _, spelling := range []string{"+8", "0x8", "0_8", "0008", "+0x08", "0x0000000000000000000000000008"} {
		wat := strings.ReplaceAll(simdLanesWat, "i8x16.shuffle 8 ", "i8x16.shuffle "+spelling+" ")
		if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := run(watPath); code != 0 {
			t.Fatalf("independent WAT parser rejects %q: %s", spelling, out)
		}
		assembled := assembleWithWatbin(t, wasmtime, asmWatPath, dir, "shuffle_spelling")
		if code, out := run(assembled); code != 0 {
			t.Fatalf("shuffle spelling %q: exit %d\n%s", spelling, code, out)
		}
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
	for _, bad := range []string{"32", "-1", "x", "-0", "0x20", "0X1", "0x", "0x_1", "1_", "1__0", "+", "184467440737095516160"} {
		invalid := `(module (memory 1) (func (export "_start")
f64.const 1.0 f64x2.splat f64.const 2.0 f64x2.splat
i8x16.shuffle ` + bad + ` 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 drop))`
		if err := os.WriteFile(watPath, []byte(invalid), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(wasmtime, "run", "--dir", dir, asmWatPath).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "shuffle byte index") {
			t.Fatalf("shuffle index %q: %v\n%s", bad, err, out)
		}
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
    i8x16.shuffle 8 9 10 11 12 13 14 15 24 25 26 27 28 29 30 31
    f64x2.extract_lane 0
    f64.const -2.5
    f64.ne
    if
      i32.const 35
      call $fail
    end
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
  (func $want (param $got i32) (param $want i32) (param $code i32)
    (if (i32.ne (local.get $got) (local.get $want)) (then (call $fail (local.get $code)))))
  (func $int_checks
    (local $a v128) (local $b v128) (local $h v128) (local $k v128) (local $w v128) (local $x v128)
    (local.set $a (i8x16.replace_lane 3 (i8x16.splat (i32.const 200)) (i32.const 7)))
    (local.set $b (i8x16.replace_lane 3 (i8x16.splat (i32.const 100)) (i32.const 250)))
    (call $want (i8x16.extract_lane_u 0 (local.get $a)) (i32.const 200) (i32.const 40))
    (call $want (i8x16.extract_lane_s 0 (local.get $a)) (i32.const -56) (i32.const 40))
    (call $want (i8x16.extract_lane_u 15 (i8x16.add (local.get $a) (local.get $b))) (i32.const 44) (i32.const 41))
    (call $want (i8x16.extract_lane_u 3 (i8x16.sub (local.get $a) (local.get $b))) (i32.const 13) (i32.const 41))
    (call $want (i8x16.extract_lane_u 0 (i8x16.add_sat_u (local.get $a) (local.get $b))) (i32.const 255) (i32.const 42))
    (call $want (i8x16.extract_lane_u 3 (i8x16.sub_sat_u (local.get $a) (local.get $b))) (i32.const 0) (i32.const 42))
    (call $want (i8x16.extract_lane_s 0 (i8x16.add_sat_s (local.get $a) (local.get $a))) (i32.const -112) (i32.const 43))
    (call $want (i8x16.extract_lane_s 0 (i8x16.sub_sat_s (local.get $b) (local.get $a))) (i32.const 127) (i32.const 43))
    (call $want (i8x16.extract_lane_u 0 (i8x16.min_u (local.get $a) (local.get $b))) (i32.const 100) (i32.const 44))
    (call $want (i8x16.extract_lane_u 0 (i8x16.max_u (local.get $a) (local.get $b))) (i32.const 200) (i32.const 44))
    (call $want (i8x16.extract_lane_u 0 (i8x16.min_s (local.get $a) (local.get $b))) (i32.const 200) (i32.const 45))
    (call $want (i8x16.extract_lane_u 0 (i8x16.max_s (local.get $a) (local.get $b))) (i32.const 100) (i32.const 45))
    (call $want (i8x16.extract_lane_u 0 (i8x16.avgr_u (local.get $a) (local.get $b))) (i32.const 150) (i32.const 46))
    (call $want (i8x16.extract_lane_s 0 (i8x16.abs (local.get $a))) (i32.const 56) (i32.const 46))
    (call $want (i8x16.extract_lane_s 0 (i8x16.neg (local.get $b))) (i32.const -100) (i32.const 46))
    (call $want (i8x16.extract_lane_u 0 (i8x16.popcnt (local.get $a))) (i32.const 3) (i32.const 46))
    (call $want (i8x16.bitmask (i8x16.ne (local.get $a) (i8x16.splat (i32.const 200)))) (i32.const 8) (i32.const 47))
    (call $want (i8x16.bitmask (i8x16.lt_u (local.get $a) (local.get $b))) (i32.const 8) (i32.const 47))
    (call $want (i8x16.bitmask (i8x16.gt_u (local.get $a) (local.get $b))) (i32.const 65527) (i32.const 47))
    (call $want (i8x16.bitmask (i8x16.le_u (local.get $a) (local.get $b))) (i32.const 8) (i32.const 47))
    (call $want (i8x16.bitmask (i8x16.ge_u (local.get $a) (local.get $b))) (i32.const 65527) (i32.const 47))
    (call $want (i8x16.bitmask (i8x16.lt_s (local.get $a) (local.get $b))) (i32.const 65527) (i32.const 48))
    (call $want (i8x16.bitmask (i8x16.gt_s (local.get $a) (local.get $b))) (i32.const 8) (i32.const 48))
    (call $want (i8x16.bitmask (i8x16.le_s (local.get $a) (local.get $b))) (i32.const 65527) (i32.const 48))
    (call $want (i8x16.bitmask (i8x16.ge_s (local.get $a) (local.get $b))) (i32.const 8) (i32.const 48))
    (call $want (i8x16.all_true (local.get $a)) (i32.const 1) (i32.const 49))
    (call $want (i8x16.all_true (i8x16.sub (local.get $a) (local.get $a))) (i32.const 0) (i32.const 49))
    (call $want (i8x16.extract_lane_u 3 (i8x16.shl (local.get $a) (i32.const 3))) (i32.const 56) (i32.const 50))
    (call $want (i8x16.extract_lane_u 0 (i8x16.shr_u (local.get $a) (i32.const 3))) (i32.const 25) (i32.const 50))
    (call $want (i8x16.extract_lane_s 0 (i8x16.shr_s (local.get $a) (i32.const 3))) (i32.const -7) (i32.const 50))
    (call $want (i8x16.extract_lane_u 0 (i8x16.swizzle (local.get $a) (i8x16.splat (i32.const 3)))) (i32.const 7) (i32.const 51))
    (local.set $h (i16x8.replace_lane 5 (i16x8.splat (i32.const 40000)) (i32.const 9)))
    (local.set $k (i16x8.splat (i32.const 30000)))
    (call $want (i16x8.extract_lane_u 0 (local.get $h)) (i32.const 40000) (i32.const 52))
    (call $want (i16x8.extract_lane_s 0 (local.get $h)) (i32.const -25536) (i32.const 52))
    (call $want (i16x8.extract_lane_u 0 (i16x8.add (local.get $h) (local.get $k))) (i32.const 4464) (i32.const 53))
    (call $want (i16x8.extract_lane_u 5 (i16x8.sub (local.get $k) (local.get $h))) (i32.const 29991) (i32.const 53))
    (call $want (i16x8.extract_lane_u 5 (i16x8.mul (local.get $h) (local.get $k))) (i32.const 7856) (i32.const 53))
    (call $want (i16x8.extract_lane_u 0 (i16x8.add_sat_u (local.get $h) (local.get $k))) (i32.const 65535) (i32.const 54))
    (call $want (i16x8.extract_lane_u 5 (i16x8.sub_sat_u (local.get $h) (local.get $k))) (i32.const 0) (i32.const 54))
    (call $want (i16x8.extract_lane_s 0 (i16x8.add_sat_s (local.get $k) (local.get $k))) (i32.const 32767) (i32.const 54))
    (call $want (i16x8.extract_lane_s 0 (i16x8.sub_sat_s (local.get $h) (local.get $k))) (i32.const -32768) (i32.const 54))
    (call $want (i16x8.extract_lane_u 0 (i16x8.min_u (local.get $h) (local.get $k))) (i32.const 30000) (i32.const 55))
    (call $want (i16x8.extract_lane_u 0 (i16x8.max_u (local.get $h) (local.get $k))) (i32.const 40000) (i32.const 55))
    (call $want (i16x8.extract_lane_u 0 (i16x8.min_s (local.get $h) (local.get $k))) (i32.const 40000) (i32.const 55))
    (call $want (i16x8.extract_lane_u 0 (i16x8.max_s (local.get $h) (local.get $k))) (i32.const 30000) (i32.const 55))
    (call $want (i16x8.extract_lane_u 0 (i16x8.avgr_u (local.get $h) (local.get $k))) (i32.const 35000) (i32.const 55))
    (call $want (i16x8.extract_lane_s 0 (i16x8.abs (local.get $h))) (i32.const 25536) (i32.const 56))
    (call $want (i16x8.extract_lane_s 0 (i16x8.neg (local.get $k))) (i32.const -30000) (i32.const 56))
    (call $want (i16x8.bitmask (i16x8.eq (local.get $h) (i16x8.splat (i32.const 9)))) (i32.const 32) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.ne (local.get $h) (i16x8.splat (i32.const 9)))) (i32.const 223) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.lt_u (local.get $h) (local.get $k))) (i32.const 32) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.gt_u (local.get $h) (local.get $k))) (i32.const 223) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.le_u (local.get $h) (local.get $k))) (i32.const 32) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.ge_u (local.get $h) (local.get $k))) (i32.const 223) (i32.const 57))
    (call $want (i16x8.bitmask (i16x8.lt_s (local.get $h) (local.get $k))) (i32.const 255) (i32.const 58))
    (call $want (i16x8.bitmask (i16x8.gt_s (local.get $h) (local.get $k))) (i32.const 0) (i32.const 58))
    (call $want (i16x8.bitmask (i16x8.le_s (local.get $h) (local.get $k))) (i32.const 255) (i32.const 58))
    (call $want (i16x8.bitmask (i16x8.ge_s (local.get $h) (local.get $k))) (i32.const 0) (i32.const 58))
    (call $want (i16x8.all_true (local.get $h)) (i32.const 1) (i32.const 59))
    (call $want (i16x8.extract_lane_u 5 (i16x8.shl (local.get $h) (i32.const 4))) (i32.const 144) (i32.const 59))
    (call $want (i16x8.extract_lane_u 0 (i16x8.shr_u (local.get $h) (i32.const 4))) (i32.const 2500) (i32.const 59))
    (call $want (i16x8.extract_lane_s 0 (i16x8.shr_s (local.get $h) (i32.const 4))) (i32.const -1596) (i32.const 59))
    (call $want (i16x8.extract_lane_u 3 (i16x8.extend_low_i8x16_u (local.get $a))) (i32.const 7) (i32.const 60))
    (call $want (i16x8.extract_lane_s 0 (i16x8.extend_low_i8x16_s (local.get $a))) (i32.const -56) (i32.const 60))
    (call $want (i16x8.extract_lane_u 0 (i16x8.extend_high_i8x16_u (local.get $b))) (i32.const 100) (i32.const 60))
    (call $want (i16x8.extract_lane_s 0 (i16x8.extend_high_i8x16_s (local.get $a))) (i32.const -56) (i32.const 60))
    (call $want (i8x16.extract_lane_u 0 (i8x16.narrow_i16x8_u (local.get $k) (local.get $h))) (i32.const 255) (i32.const 61))
    (call $want (i8x16.extract_lane_u 13 (i8x16.narrow_i16x8_u (local.get $k) (local.get $h))) (i32.const 9) (i32.const 61))
    (call $want (i8x16.extract_lane_s 8 (i8x16.narrow_i16x8_s (local.get $k) (local.get $h))) (i32.const -128) (i32.const 61))
    (local.set $w (i32x4.replace_lane 2 (i32x4.splat (i32.const 3000000000)) (i32.const 5)))
    (local.set $x (i32x4.splat (i32.const 70000)))
    (call $want (i32x4.extract_lane 2 (i32x4.add (local.get $w) (local.get $x))) (i32.const 70005) (i32.const 62))
    (call $want (i32x4.extract_lane 2 (i32x4.sub (local.get $x) (local.get $w))) (i32.const 69995) (i32.const 62))
    (call $want (i32x4.extract_lane 2 (i32x4.mul (local.get $w) (local.get $x))) (i32.const 350000) (i32.const 62))
    (call $want (i32x4.extract_lane 0 (i32x4.min_u (local.get $w) (local.get $x))) (i32.const 70000) (i32.const 63))
    (call $want (i32x4.extract_lane 0 (i32x4.max_u (local.get $w) (local.get $x))) (i32.const 3000000000) (i32.const 63))
    (call $want (i32x4.extract_lane 0 (i32x4.min_s (local.get $w) (local.get $x))) (i32.const 3000000000) (i32.const 63))
    (call $want (i32x4.extract_lane 0 (i32x4.max_s (local.get $w) (local.get $x))) (i32.const 70000) (i32.const 63))
    (call $want (i32x4.extract_lane 0 (i32x4.abs (local.get $w))) (i32.const 1294967296) (i32.const 64))
    (call $want (i32x4.extract_lane 0 (i32x4.neg (local.get $x))) (i32.const -70000) (i32.const 64))
    (call $want (i32x4.bitmask (i32x4.eq (local.get $w) (i32x4.splat (i32.const 5)))) (i32.const 4) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.ne (local.get $w) (i32x4.splat (i32.const 5)))) (i32.const 11) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.lt_u (local.get $w) (local.get $x))) (i32.const 4) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.gt_u (local.get $w) (local.get $x))) (i32.const 11) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.le_u (local.get $w) (local.get $x))) (i32.const 4) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.ge_u (local.get $w) (local.get $x))) (i32.const 11) (i32.const 65))
    (call $want (i32x4.bitmask (i32x4.lt_s (local.get $w) (local.get $x))) (i32.const 15) (i32.const 66))
    (call $want (i32x4.bitmask (i32x4.gt_s (local.get $w) (local.get $x))) (i32.const 0) (i32.const 66))
    (call $want (i32x4.bitmask (i32x4.le_s (local.get $w) (local.get $x))) (i32.const 15) (i32.const 66))
    (call $want (i32x4.bitmask (i32x4.ge_s (local.get $w) (local.get $x))) (i32.const 0) (i32.const 66))
    (call $want (i32x4.all_true (local.get $w)) (i32.const 1) (i32.const 67))
    (call $want (i32x4.extract_lane 2 (i32x4.shl (local.get $w) (i32.const 4))) (i32.const 80) (i32.const 67))
    (call $want (i32x4.extract_lane 0 (i32x4.shr_u (local.get $w) (i32.const 4))) (i32.const 187500000) (i32.const 67))
    (call $want (i32x4.extract_lane 0 (i32x4.shr_s (local.get $w) (i32.const 4))) (i32.const -80935456) (i32.const 67))
    (call $want (i32x4.extract_lane 0 (i32x4.extend_low_i16x8_u (local.get $h))) (i32.const 40000) (i32.const 68))
    (call $want (i32x4.extract_lane 0 (i32x4.extend_low_i16x8_s (local.get $h))) (i32.const -25536) (i32.const 68))
    (call $want (i32x4.extract_lane 1 (i32x4.extend_high_i16x8_u (local.get $h))) (i32.const 9) (i32.const 68))
    (call $want (i32x4.extract_lane 0 (i32x4.extend_high_i16x8_s (local.get $h))) (i32.const -25536) (i32.const 68))
    (call $want (i16x8.extract_lane_u 0 (i16x8.narrow_i32x4_u (local.get $x) (local.get $w))) (i32.const 65535) (i32.const 69))
    (call $want (i16x8.extract_lane_u 6 (i16x8.narrow_i32x4_u (local.get $x) (local.get $w))) (i32.const 5) (i32.const 69))
    (call $want (i16x8.extract_lane_s 4 (i16x8.narrow_i32x4_s (local.get $x) (local.get $w))) (i32.const -32768) (i32.const 69))
    (call $want (i32x4.extract_lane 0 (i32x4.dot_i16x8_s (local.get $k) (local.get $k))) (i32.const 1800000000) (i32.const 70)))
  (func $main
    (local $a v128) (local $b v128) (local $m v128) (local $c v128) (local $s v128)
    (local.set $a (f64x2.replace_lane 1 (f64x2.splat (f64.const 3.0)) (f64.const -2.5)))
    (local.set $b (f64x2.replace_lane 1 (f64x2.splat (f64.const 1.5)) (f64.const 4.0)))
    (if (i32.eqz (call $f64_eq (f64x2.extract_lane 1 (i8x16.shuffle 8 9 10 11 12 13 14 15 24 25 26 27 28 29 30 31 (local.get $a) (local.get $b))) (f64.const 4.0))) (then (call $fail (i32.const 36))))
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
    (call $int_checks)
    (call $exit (i32.const 0)))
  (export "_start" (func $main)))
`

package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPackedLaneBounds(t *testing.T) {
	for _, target := range []string{e2eharness.HostSelfHostTarget(), e2eharness.TargetWasm32Wasi} {
		if target == "" {
			continue
		}
		if target == e2eharness.TargetWasm32Wasi {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				continue
			}
		}
		for _, width := range []int{32, 64} {
			for _, store := range []bool{false, true} {
				for _, offset := range []int{-1, 0, 16, 17, 2147483647} {
					name := fmt.Sprintf("%s/%d/store=%t/offset=%d", target, width, store, offset)
					t.Run(name, func(t *testing.T) {
						prefix := fmt.Sprintf("__f%dx%d_", width, 128/width)
						op := fmt.Sprintf("let v = %sload_bytes(xs, %d); return %sbitmask(v);", prefix, offset, prefix)
						if store {
							op = fmt.Sprintf("let out = %swith_bytes(xs, %d, %ssplat(1.0)); return out[0] as i32;", prefix, offset, prefix)
						}
						source := "function main(): i32 { let xs = __alloc_u8(32); " + op + " }"
						dir := t.TempDir()
						entry := filepath.Join(dir, "main.fern")
						bin := filepath.Join(dir, "program")
						if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
							t.Fatal(err)
						}
						cmd := e2eharness.SelfHostCompileCmd(t, target, entry, bin)
						cmd.Env = e2eharness.SelfHostChildEnv()
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("compile: %v\n%s", err, out)
						}
						run := exec.Command(bin)
						if target == e2eharness.TargetWasm32Wasi {
							run = e2eharness.RunWasmCore(t, bin)
						}
						out, err := run.CombinedOutput()
						valid := offset == 0 || offset == 16
						if valid && err != nil {
							t.Fatalf("valid access failed: %v\n%s", err, out)
						}
						if !valid && err == nil {
							t.Fatalf("access beyond the 16-byte window succeeded")
						}
					})
				}
			}
		}
	}
}

func TestSelfHostRawLaneStores(t *testing.T) {
	const source = `function main(): i32 {
  unsafe {
   let p = __alloc(40);
   __store_u8(p, 37);
   __store_u8(p + 33 as usize, 19);
   __f32x4_store(p + 1 as usize, __f32x4_of(1.0 as f32, 2.0 as f32, 3.0 as f32, 4.0 as f32));
   let f32 = __f32x4_load_ptr(p + 1 as usize);
   let a = f32_bits(__f32x4_lane(f32, 0));
   let last = __load_i32(p + 13 as usize);
   __f64x2_store(p + 17 as usize, __f64x2_of(1.0, -0.0));
   let f64 = __f64x2_load_ptr(p + 17 as usize);
   let b = f64_bits(__f64x2_lane(f64, 0));
   let sign = f64_bits(__f64x2_lane(f64, 1));
   let first_guard = __load_u8(p);
   let last_guard = __load_u8(p + 33 as usize);
   __free(p, 40);
   if (a != 1065353216 || last != 1082130432 || b != 4607182418800017408i64 || sign != f64_bits(-0.0) || first_guard != 37 || last_guard != 19) { return 1; }
  }
  return 0;
 }`
	for _, target := range []string{e2eharness.HostSelfHostTarget(), e2eharness.TargetWasm32Wasi} {
		if target == "" {
			continue
		}
		if target == e2eharness.TargetWasm32Wasi {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				continue
			}
		}
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.fern")
			bin := filepath.Join(dir, "program")
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := e2eharness.SelfHostCompileCmd(t, target, entry, bin)
			cmd.Env = e2eharness.SelfHostChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := exec.Command(bin)
			if target == e2eharness.TargetWasm32Wasi {
				run = e2eharness.RunWasmCore(t, bin)
			}
			if out, err := run.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}

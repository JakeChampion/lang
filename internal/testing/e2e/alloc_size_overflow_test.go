package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestAllocSizeOverflowAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	t.Run("repeat_product_wraps_negative", func(t *testing.T) {
		assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  let s: string = "abcdefgh";
  let r: string = s.repeat(300000000);
  return r.len();
}
`, "allocation size out of range")
	})
	t.Run("concat_combined_length_wraps_negative", func(t *testing.T) {
		assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  let a: string = "x";
  let i: i32 = 0;
  while (i < 30) { a = a + a; i = i + 1; }
  let b: string = a + a;
  return b.len();
}
`, "allocation size out of range")
	})
}

func TestAllocSizeWrappingToZeroStillAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  let s: string = "abcd";
  let r: string = s.repeat(1073741824);
  return r.len();
}
`, "allocation size out of range")
}

func TestLargeButRepresentableAllocationStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	src := `import "std/string";
function main(): i32 {
  let a: string = "x";
  let i: i32 = 0;
  while (i < 20) { a = a + a; i = i + 1; }
  if (a.len() != 1048576) { return 7; }
  return 0;
}
`
	t.Run("x86_64", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, src); code != 0 {
			t.Errorf("x86_64 exited %d, want 0; out=%q", code, out)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, src); code != 0 {
			t.Errorf("arm64 exited %d, want 0; out=%q", code, out)
		}
	})
}

func assertAbortsWithCause(t *testing.T, src, cause string) {
	t.Helper()
	t.Run("x86_64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, src)
		checkAbort(t, "x86_64", out, code, cause, src)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, code := compileAndRunArm64(t, src)
		checkAbort(t, "arm64", out, code, cause, src)
	})
}

func checkAbort(t *testing.T, backend, out string, code int, cause, src string) {
	t.Helper()
	if code != 134 {
		t.Errorf("%s exited %d, want 134; out=%q\nsrc:\n%s", backend, code, out, src)
	}
	if !strings.Contains(out, cause) {
		t.Errorf("%s aborted without the %q cause line; out=%q\nsrc:\n%s", backend, cause, out, src)
	}
	if trimOut(withoutAbortDiag(out)) != "" {
		t.Errorf("%s printed %q before aborting\nsrc:\n%s", backend, out, src)
	}
}

func TestArrayGrowSizeOverflowAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	src := `function main(): i32 {
  let a: u8[] = __alloc_u8(1073741824);
  a = a.append(7 as u8);
  return a.len();
}
`
	t.Run("wasm32-wasi", func(t *testing.T) {
		holdingMemoryMB(t, 1200, func() {
			out, stderr, code := runWasmArtifact(t, buildCLIComponent(t, src), runOpts{})
			if code == 0 {
				t.Errorf("wasm did not trap (exit 0); stdout=%q", out)
			}
			if !strings.Contains(stderr, "unreachable") {
				t.Errorf("wasm trapped somewhere other than the size check; stderr=%q", stderr)
			}
			if trimOut(out) != "" {
				t.Errorf("wasm printed %q before trapping", out)
			}
		})
	})
}

func TestLargeButRepresentableArrayGrowStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	src := `function main(): i32 {
  let a: u8[] = __alloc_u8(300000000);
  a = a.append(7 as u8);
  if (a.len() != 300000001) { return 1; }
  return (a[300000000] as i32) - 7;
}
`
	holdingMemoryMB(t, 1000, func() {
		t.Run("x86_64", func(t *testing.T) {
			if out, code := compileAndRunX86_64(t, src); code != 0 {
				t.Errorf("x86_64 exited %d, want 0; out=%q", code, out)
			}
		})
		t.Run("arm64-linux", func(t *testing.T) {
			if out, code := compileAndRunArm64(t, src); code != 0 {
				t.Errorf("arm64 exited %d, want 0; out=%q", code, out)
			}
		})
		t.Run("wasm32-wasi", func(t *testing.T) {
			if got := runWasm(t, src); got != 0 {
				t.Errorf("wasm returned %d, want 0", got)
			}
		})
	})
}

func holdingMemoryMB(t *testing.T, weightMB int, fn func()) {
	t.Helper()
	if err := e2eharness.WithBuildMemoryMB(weightMB, func() error {
		fn()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

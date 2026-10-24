package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const vdsoClockSource = `import "std/i64";

function main(): i32 {
  let first: i64 = monotonic_ns();
  let prev: i64 = first;
  let i: i32 = 0;
  while (i < 100000) {
    let now: i64 = monotonic_ns();
    if (now < prev) {
      return 2;
    }
    prev = now;
    i = i + 1;
  }
  if (prev <= first) {
    return 3;
  }
  let ms: i64 = now_unix_ms();
  let ns: i64 = now_ns();
  let apart: i64 = ns / 1000000 as i64 - ms;
  if (apart < 0 as i64 || apart > 1000 as i64) {
    return 4;
  }
  eprint(ms.to_string());
  return 7;
}
`

func TestSelfHostVdsoClock(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(vdsoClockSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{".Lfern_auxv_scan", "__fn___fern_vdso_clock:", "__fn___c_call2:", "__fern_vdso:"} {
				if !strings.Contains(string(asm), want) {
					t.Errorf("%s asm lacks %q", target, want)
				}
			}
			before := time.Now().UnixMilli()
			stderr, code := cli.exitOf(t, vdsoClockSource, target)
			after := time.Now().UnixMilli()
			if code != 7 {
				t.Fatalf("exit %d, want 7 (2 = monotonic went back, 3 = it never advanced, 4 = now_ns and now_unix_ms disagree)\n%s", code, stderr)
			}
			wall, err := strconv.ParseInt(strings.TrimSpace(stderr), 10, 64)
			if err != nil {
				t.Fatalf("stderr %q is not the wall clock: %v", stderr, err)
			}
			if wall < before-1000 || wall > after+1000 {
				t.Errorf("now_unix_ms %d is outside the test's [%d, %d]", wall, before, after)
			}
		})
	}
	t.Run("arm64-darwin", func(t *testing.T) {
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-darwin"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asm), "__fern_vdso") {
			t.Error("arm64-darwin asm references the vDSO slot; XNU has no vDSO")
		}
	})
}

package e2ecompiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostStringArrayReleaseRuntime(t *testing.T) {
	h := selfHostCLIForHost(t)
	// These raw boxes deliberately forge counts and release a handle twice.
	// The runtime probes bypass the certifier, as the sanitizer tests do.
	const src = `function element(): usize {
  let data: usize = __alloc(8);
  __store_i64(data, 123);
  let base: usize = __alloc(24);
  __raw_store_ptr(base, 0, 1 as usize);
  __raw_store_ptr(base, 1, data);
  __raw_store_ptr(base, 2, 8 as usize);
  return base + 8;
}
function row(p: usize): usize {
  let a: usize = __raw_arr_box(1);
  __raw_store_ptr(a, 1, p);
  return a;
}
function main(): i32 {
  if (args().len() > 1) {
    let bad: usize = __raw_arr_box(0);
    if (args()[1] == "zero-count") { __store_i32(bad - 8, 0); }
    else { __fern_str_arr_free(bad); }
    __fern_str_arr_free(bad);
    return 9;
  }
  __fern_str_arr_free(0 as usize);
  __fern_str_arr_free(1 as usize);
  __fern_str_arr_free(65535 as usize);
  let p: usize = element();
  let a: usize = row(p);
  __store_i32(a - 8, 2);
  __fern_str_arr_free(a);
  if (__load_i32(a - 8) != 1 || __load_i32(p - 8) != 1) { return 1; }
  __fern_str_arr_free(a);
  let shared: usize = element();
  __store_i32(shared - 8, 2);
  __fern_str_arr_free(row(shared));
  if (__load_i32(shared - 8) != 1) { return 2; }
  __fern_str_free(shared);
  let duplicate: usize = element();
  __store_i32(duplicate - 8, 2);
  let two: usize = __raw_arr_box(2);
  __raw_store_ptr(two, 1, duplicate);
  __raw_store_ptr(two, 2, duplicate);
  __fern_str_arr_free(two);
  let kept: usize = element();
  let immortal: usize = row(kept);
  __store_i32(immortal - 8, 0 - 1);
  __fern_str_arr_free(immortal);
  if (__load_i32(kept - 8) != 1) { return 3; }
  __store_i32(immortal - 8, 1);
  __fern_str_arr_free(immortal);
  __fern_str_arr_free(row(0 as usize));
  __fern_str_arr_free(__raw_arr_box(0));
  let large: usize = __raw_arr_box(65536);
  __raw_store_ptr(large, 0, 0 as usize);
  __fern_str_arr_free(large);
  let i: i32 = 0;
  while (i < 1000) { __fern_str_arr_free(row(element())); i = i + 1; }
  return 0;
}`
	dir := t.TempDir()
	path := filepath.Join(dir, "string_array_release.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		t.Run(target+"/emit", func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "program.s")
			cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, path, h.stdlib)
			cmd.Env = append(childEnv(), uncertified)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("emit: %v\n%s", err, output)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			asm := string(raw)
			if !strings.Contains(asm, "Lssa___fern_str_arr_free_") || strings.Contains(asm, "Lsaf_loop") {
				t.Error("string-array release must use the Fern SSA body")
			}
		})
	}
	for _, tg := range h.targets {
		if len(tg.runner) != 0 {
			continue
		}
		for _, flags := range [][]string{nil, {"-sanitize"}} {
			t.Run(tg.target+"/run"+strings.Join(flags, ""), func(t *testing.T) {
				bin := filepath.Join(t.TempDir(), "program")
				args := append([]string{"-target", tg.target, "-o", bin}, flags...)
				args = append(args, path, h.stdlib)
				cmd := exec.Command(h.cli, args...)
				cmd.Env = append(childEnv(), uncertified)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				out, err := exec.CommandContext(ctx, bin).CombinedOutput()
				if err != nil {
					t.Fatalf("string-array release: %v\n%s", err, out)
				}
				if len(flags) > 0 {
					assertBalancedCensus(t, string(out))
					for _, bad := range []struct{ arg, report string }{
						{"double-drop", "use-after-free (touched a quarantined block)"},
						{"zero-count", "rc over-release (double free)"},
					} {
						run := exec.CommandContext(ctx, bin, bad.arg)
						output, _ := run.CombinedOutput()
						if run.ProcessState == nil || run.ProcessState.ExitCode() != e2eharness.ExitSanitizer || !strings.Contains(string(output), "fern-sanitizer: "+bad.report) {
							t.Errorf("%s did not abort with its sanitizer report:\n%s", bad.arg, output)
						}
					}
				}
			})
		}
	}
}

package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each Z holds a head of 32768 elements, through a `with` write and a store
// back. Each caller of `pick` gives it at least 256 elements, through a phi
// of two lengths. `zeros` gives back as many elements as its parameter, and
// `like` as many as the length of its parameter.
const fieldLenProg = `struct Z { head: u16[], n: i32 }
@noinline function zeros(n: i32): i32[] {
  let out = __alloc_i32(n);
  for i in 0..n {
    out = out.with(i, 0);
  }
  return out;
}
@noinline function like(xs: i32[]): i32[] {
  return zeros(xs.len());
}
@noinline function sized(x: i32): i32 {
  let a = zeros(64);
  let b = like(a);
  return a[x & 63] + b[x & 31];
}
function make(): Z {
  return Z { head: __alloc_u16(32768), n: 0 };
}
@noinline function step(own z: Z, x: i32): Z {
  let head = z.head;
  let h = x & 32767;
  head = head.with(h, head[h] + 1u16);
  return Z { ...z, head: head, n: z.n + 1 };
}
@noinline function pick(t: i32[], x: i32): i32 {
  return t[x & 255];
}
function main(): i32 {
  let z = make();
  for i in 0..1000 {
    z = step(z, i * 7919);
  }
  let t = if (args().len() > 5) { __alloc_i32(300) } else { __alloc_i32(256) };
  t = t.with(3, 9);
  print((z.head[7919 & 32767] as i32 + z.n + pick(t, 259) + sized(1000)).to_string());
  return 0;
}
`

// One W holds a head of 16 elements, so `step` keeps its check, and the run
// aborts. One caller gives `pick` 100 elements, so `pick` keeps its check.
// `zeros` gives `sized` 32 elements, below the mask.
const fieldLenShortProg = `struct W { head: u16[] }
@noinline function zeros(n: i32): i32[] {
  let out = __alloc_i32(n);
  for i in 0..n {
    out = out.with(i, 0);
  }
  return out;
}
@noinline function sized(x: i32): i32 {
  return zeros(32)[x & 63];
}
@noinline function step(w: W, x: i32): i32 {
  return w.head[x & 32767] as i32;
}
@noinline function pick(t: i32[], x: i32): i32 {
  return t[x & 255];
}
function main(): i32 {
  let long = W { head: __alloc_u16(32768) };
  let short = W { head: __alloc_u16(16) };
  print((step(long, 40000) + pick(__alloc_i32(256), 3) + pick(__alloc_i32(100), 3) + sized(5)).to_string());
  print(step(short, 1000).to_string());
  return 0;
}
`

func TestSelfHostFieldLength(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	cases := []struct {
		name, src string
		checked   map[string]bool
		wantOut   string
		wantAbort bool
	}{
		{"filled", fieldLenProg, map[string]bool{"step": false, "pick": false, "sized": false}, "1010\n", false},
		{"short", fieldLenShortProg, map[string]bool{"step": true, "pick": true, "sized": true}, "0\n", true},
	}
	for _, c := range cases {
		src := filepath.Join(dir, c.name+".fern")
		if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			out := filepath.Join(dir, c.name+"-"+target+".s")
			if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("%s %s: emitting: %v\n%s", c.name, target, err, combined)
			}
			asm, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for fn, want := range c.checked {
				if got := strings.Contains(condBranchBody(t, string(asm), fn), "oob_abort"); got != want {
					t.Errorf("%s %s: %s keeps a bounds check: %v, want %v", c.name, target, fn, got, want)
				}
			}
		}
		for _, tg := range h.targets {
			bin := filepath.Join(dir, c.name+"-"+tg.target+".bin")
			if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("%s %s: building: %v\n%s", c.name, tg.target, err, combined)
			}
			cmd := exec.Command(bin)
			if len(tg.runner) > 0 {
				cmd = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			got, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()
			if c.wantAbort {
				if code != 134 || !strings.HasPrefix(string(got), c.wantOut) || !strings.Contains(string(got), "array index out of range") {
					t.Errorf("%s %s: exit %d, output %q; want %q and the abort of the short head", c.name, tg.target, code, got, c.wantOut)
				}
			} else if code != 0 || string(got) != c.wantOut {
				t.Errorf("%s %s: exit %d, output %q; want %q", c.name, tg.target, code, got, c.wantOut)
			}
		}
	}
}

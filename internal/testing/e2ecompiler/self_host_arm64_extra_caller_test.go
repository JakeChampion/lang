package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestSelfHostArm64ExtraCallerRegisters(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for name, source := range map[string]string{
		"runtime-argument-cycle":  armRuntimeArgumentCycle,
		"float-argument-boundary": armFloatArgumentBoundary,
		"link-register-home":      armLinkRegisterHome,
		"paired-result-pressure":  armPairedResultPressure,
	} {
		t.Run(name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, "arm64-linux"); code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
		})
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(armRuntimeArgumentCycle), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(asm), "wrap")
	if regexp.MustCompile(`(?m)^\s+(?:stp|str)\s+x(?:19|2[0-8])\b`).MatchString(body) {
		t.Fatalf("short-lived parameters unnecessarily consume callee-saved registers:\n%s", body)
	}
	src = filepath.Join(t.TempDir(), "leaf.fern")
	if err := os.WriteFile(src, []byte(armLinkRegisterHome), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err = os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body = tailCallBody(t, string(asm), "sum13")
	if !regexp.MustCompile(`(?m)^\s+(?:ldr|mov)\s+x30,`).MatchString(body) {
		t.Fatalf("the pressure case no longer exercises a link-register home:\n%s", body)
	}
	if !regexp.MustCompile(`(?m)^\s+stp x29, x30,`).MatchString(body) {
		t.Fatalf("a link-register home overwrites the unsaved return address:\n%s", body)
	}

	t.Run("link-register-home-instrumented", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		t.Setenv("QEMU_CPU", "max")
		for _, mode := range []string{"", "cpu", "alloc"} {
			for _, harden := range []bool{false, true} {
				label := mode
				if label == "" {
					label = "plain"
				}
				if harden {
					label += "-harden"
				}
				t.Run(label, func(t *testing.T) {
					var flags []string
					if harden {
						flags = append(flags, "-harden")
					}
					bin := compileCPUProfile(t, cli, armLinkRegisterHome, "arm64-linux", mode, flags...)
					if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
						t.Fatalf("link-register return failed: %v: %s", err, out)
					}
				})
			}
		}
	})

}

const armRuntimeArgumentCycle = `@noinline function wrap(n: i32, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, p: usize): string {
  __store_i64(p, b);
  __store_i64(p + 8, c);
  __store_i64(p + 16, d);
  __store_i64(p + 24, e);
  __store_i64(p + 32, f);
  __store_i64(p + 40, g);
  __store_i64(p + 48, h);
  return __raw_string(p, n);
}
function main(): i32 {
  let p = __raw_alloc(64);
  let s = wrap(3, 6513249i64, 2i64, 3i64, 4i64, 5i64, 6i64, 7i64, p);
  if (__raw_load8(p, 48) != 7) { return 1; }
  if (s != "abc") { return 2; }
  return 0;
}
`

const armFloatArgumentBoundary = `@noinline function sum9(a: f64, b: f64, c: f64, d: f64, e: f64, f: f64, g: f64, h: f64, i: f64): f64 {
  return a + b + c + d + e + f + g + h + i;
}
@noinline function indirect(f: (f64, f64, f64, f64, f64, f64, f64, f64, f64) => f64, x: f64): f64 {
  return f(1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0) + x;
}
function main(): i32 {
  let salt = args().len() as f64;
  let got = indirect(sum9, salt + 10.5);
  if (got != salt + 55.5) { return 1; }
  return 0;
}
`

const armLinkRegisterHome = `@noinline function sum13(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, i: i64, j: i64, k: i64, l: i64, m: i64): i64 {
  return a + b + c + d + e + f + g + h + i + j + k + l + m;
}
function main(): i32 {
  let seed = args().len() as i64;
  let result = sum13(seed, seed + 1, seed + 2, seed + 3, seed + 4, seed + 5, seed + 6, seed + 7, seed + 8, seed + 9, seed + 10, seed + 11, seed + 12);
  if (result != seed * 13 + 78) { return 1; }
  return 0;
}
`

const armPairedResultPressure = `@noinline function pair13(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, i: i64, j: i64, k: i64, l: i64, m: i64): (i64, i32) {
  return (a+b+c+d+e+f+g+h+i+j+k+l+m,77);
}
@noinline function caller13(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, i: i64, j: i64, k: i64, l: i64, m: i64): i64 {
  let (value,tag) = pair13(m,l,k,j,i,h,g,f,e,d,c,b,a);
  return value + (tag as i64);
}
function main(): i32 {
  let seed = args().len() as i64;
  let got = caller13(seed,seed+1,seed+2,seed+3,seed+4,seed+5,seed+6,seed+7,seed+8,seed+9,seed+10,seed+11,seed+12);
  if (got != seed*13+155) { return 1; }
  return 0;
}
`

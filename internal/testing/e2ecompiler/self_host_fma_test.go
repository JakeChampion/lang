package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// poly makes six fused multiply-adds. The conformance case f64_fma checks
// their values on each backend.
const fmaPolySrc = `import "std/f64";

@noinline
function poly(x: f64, c0: f64, c1: f64, c2: f64, c3: f64): f64 {
  let a = __fma_f64(c3, x, c2);
  a = __fma_f64(a, x, c1);
  a = __fma_f64(x, a, c0);
  let b = __fma_f64(a, a, x);
  return __fma_f64(b, x, a) + __fma_f64(c0, c1, b);
}

function main(): i32 {
  print(poly(1.0000001, 0.1, 0.2, 0.3, 0.4).to_string());
  return 0;
}
`

// fmaSection gives the lines after the line that starts with start, up to the
// first line after it that contains end.
func fmaSection(t *testing.T, asm, start, end string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), start) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.Contains(lines[j], end) {
				return strings.Join(lines[i+1:j], "\n")
			}
		}
	}
	t.Fatalf("no %q section in the emitted code", start)
	return ""
}

// Each native backend gives __fma_f64 one fused instruction, and wasm calls
// the software form of the runtime, because wasm has no fused multiply-add.
func TestSelfHostFmaF64Emission(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(fmaPolySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, start, end, fused, separate string
	}{
		{e2eharness.TargetX86_64Linux, "__fn_poly:", ".cfi_endproc", "vfmadd", "mulsd"},
		{e2eharness.TargetArm64Linux, "__fn_poly:", ".cfi_endproc", "fmadd d", "fmul"},
		{e2eharness.TargetWasm32Wasi, "(func $poly ", "(func ", "call $__fern_fma_f64", "f64.mul"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), c.target, src)
			body := fmaSection(t, asm, c.start, c.end)
			if n := strings.Count(body, c.fused); n != 6 {
				t.Fatalf("poly has %d of %q, want 6:\n%s", n, c.fused, body)
			}
			if strings.Contains(body, c.separate) {
				t.Fatalf("poly rounds a product with %q:\n%s", c.separate, body)
			}
		})
	}
}

// std/f64 calls __fma_f64 for fma, mul_add and the seven fused
// multiply-adds of the table path of log. f64_special and
// float_clamp01_absdiff_muladd check the values.
const stdF64FmaSrc = `import "std/f64";

@noinline
function fused(x: f64, y: f64, z: f64): f64 {
  return x.fma(y, z) + y.mul_add(z, x);
}

function main(): i32 {
  print(fused(0.1, 10.0, -1.0).to_string());
  print(3.0.log().to_string());
  return 0;
}
`

func TestSelfHostStdF64FmaEmission(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(stdF64FmaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, fusedStart, logStart, end, fma, separate string
	}{
		{e2eharness.TargetX86_64Linux, "__fn_fused:", "__fn_f64___fd_log_main:", ".cfi_endproc", "vfmadd", "mulsd"},
		{e2eharness.TargetArm64Linux, "__fn_fused:", "__fn_f64___fd_log_main:", ".cfi_endproc", "fmadd d", "fmul"},
		{e2eharness.TargetWasm32Wasi, "(func $fused ", "(func $f64___fd_log_main ", "(func ", "call $__fern_fma_f64", "f64.mul"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), c.target, src)
			body := fmaSection(t, asm, c.fusedStart, c.end)
			if n := strings.Count(body, c.fma); n != 2 {
				t.Fatalf("fused has %d of %q, want 2:\n%s", n, c.fma, body)
			}
			if strings.Contains(body, c.separate) {
				t.Fatalf("fused rounds a product with %q:\n%s", c.separate, body)
			}
			body = fmaSection(t, asm, c.logStart, c.end)
			if n := strings.Count(body, c.fma); n != 7 {
				t.Fatalf("_fd_log_main has %d of %q, want 7:\n%s", n, c.fma, body)
			}
		})
	}
}

// In chain, each sum stays across the loop in the register of the first
// factor, of the second factor and of the addend. In pick, the result of a
// select stays in the register of its first choice. The conformance cases
// lanes_f64x2_fma and lanes_select_in_place check the values.
const laneTernarySrc = `function show(tag: string, v: f64x2): void {
  print(tag + " " + __f64x2_lane(v, 0).to_string() + " " + __f64x2_lane(v, 1).to_string());
}

@noinline
function chain(x: f64x2, k: f64x2, n: i32): f64x2 {
  let a = x;
  let b = x;
  let c = k;
  let i = 0;
  while (i < n) {
    a = __f64x2_mul_add(a, x, k);
    b = __f64x2_mul_add(x, b, k);
    c = __f64x2_mul_add(x, k, c);
    i += 1;
  }
  return a + b + c;
}

@noinline
function pick(x: f64x2, y: f64x2, n: i32): f64x2 {
  let a = x;
  let i = 0;
  while (i < n) {
    a = __f64x2_select(a, y, __f64x2_lt(__f64x2_splat(i as f64), __f64x2_of(2.0, 4.0)));
    i += 1;
  }
  return a;
}

function main(): i32 {
  show("chain", chain(__f64x2_of(0.5, 1.5), __f64x2_of(0.25, -0.75), 5));
  show("pick", pick(__f64x2_of(1.0, 5.0), __f64x2_of(3.0, 2.0), 3));
  return 0;
}
`

// laneOperands gives the register operands of each line of body whose
// mnemonic starts with mnem, without a lane arrangement such as ".2d".
func laneOperands(body, mnem string) [][]string {
	var out [][]string
	for _, ln := range strings.Split(body, "\n") {
		fields := strings.Fields(ln)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], mnem) {
			continue
		}
		ops := strings.Split(strings.Join(fields[1:], ""), ",")
		for i, op := range ops {
			ops[i], _, _ = strings.Cut(op, ".")
		}
		out = append(out, ops)
	}
	return out
}

// A lane fused multiply-add whose result register is also the register of a
// factor must read that factor before it writes the register. On x86-64 the
// result takes vfmadd213pd, which multiplies its destination, and on arm64 fmla
// accumulates into the scratch v0. No fused instruction names its destination
// as a source. Thus the addend never replaces a factor. A select on arm64 whose
// result holds its first choice takes bif.
func TestSelfHostLaneFmaEmission(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneTernarySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := e2eharness.SelfHostCLI(t)
	t.Run(e2eharness.TargetX86_64Linux, func(t *testing.T) {
		body := fmaSection(t, e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetX86_64Linux, src), "__fn_chain:", ".cfi_endproc")
		fused := laneOperands(body, "vfmadd")
		if len(fused) != 3 || strings.Count(body, "vfmadd213pd") != 2 || strings.Count(body, "vfmadd231pd") != 1 {
			t.Fatalf("chain wants two vfmadd213pd for the sums in a factor and one vfmadd231pd for the sum in the addend:\n%s", body)
		}
		for _, ops := range fused {
			if ops[2] == ops[0] || ops[2] == ops[1] {
				t.Errorf("the destination %s is also a source of the fused instruction:\n%s", ops[2], body)
			}
		}
		if strings.Contains(body, "mulpd") {
			t.Errorf("chain rounds a product with mulpd:\n%s", body)
		}
	})
	t.Run(e2eharness.TargetArm64Linux, func(t *testing.T) {
		asm := e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetArm64Linux, src)
		body := fmaSection(t, asm, "__fn_chain:", ".cfi_endproc")
		lines := strings.Split(body, "\n")
		inFactor, inPlace := 0, 0
		for i, ln := range lines {
			ops := laneOperands(ln, "fmla")
			if len(ops) == 0 {
				continue
			}
			d := ops[0][0]
			if d == ops[0][1] || d == ops[0][2] {
				t.Errorf("fmla names its destination %s as a factor:\n%s", d, body)
			}
			if d != "v0" {
				inPlace++
				continue
			}
			if i+1 < len(lines) {
				if home := laneOperands(lines[i+1], "mov"); len(home) == 1 && home[0][1] == "v0" && (home[0][0] == ops[0][1] || home[0][0] == ops[0][2]) {
					inFactor++
				}
			}
		}
		if inFactor != 2 || inPlace != 1 {
			t.Fatalf("chain wants two fmla through v0 into the register of a factor and one fmla in place, got %d and %d:\n%s", inFactor, inPlace, body)
		}
		if strings.Contains(body, "fmul") {
			t.Errorf("chain rounds a product with fmul:\n%s", body)
		}
		pick := fmaSection(t, asm, "__fn_pick:", ".cfi_endproc")
		if !strings.Contains(pick, "bif ") || strings.Contains(pick, "bsl ") {
			t.Errorf("pick wants bif, which keeps the first choice in the result register:\n%s", pick)
		}
	})
	t.Run(e2eharness.TargetWasm32Wasi, func(t *testing.T) {
		body := fmaSection(t, e2eharness.EmitAsmWithSelfHost(t, cli, e2eharness.TargetWasm32Wasi, src), "(func $chain ", "(func ")
		if n := strings.Count(body, "call $__fern_fma_f64"); n != 6 {
			t.Fatalf("chain has %d calls of the software fma, want 6, one for each lane:\n%s", n, body)
		}
		if strings.Contains(body, "f64x2.mul") {
			t.Errorf("chain rounds a product with f64x2.mul:\n%s", body)
		}
	})
}

// poly32 makes six fused multiply-adds of f32 values, and lanes makes one of
// f32x4. The conformance case f32_fma checks their values on each backend.
const fma32PolySrc = `import "std/f32";

@noinline
function poly32(x: f32, c0: f32, c1: f32, c2: f32, c3: f32): f32 {
  let a = __fma_f32(c3, x, c2);
  a = __fma_f32(a, x, c1);
  a = __fma_f32(x, a, c0);
  let b = __fma_f32(a, a, x);
  return __fma_f32(b, x, a) + __fma_f32(c0, c1, b);
}

@noinline
function lanes(x: f32, y: f32, z: f32): f32 {
  return __f32x4_lane(__f32x4_mul_add(__f32x4_splat(x), __f32x4_splat(y), __f32x4_splat(z)), 1);
}

function main(): i32 {
  let t = 0.1 as f32;
  print(poly32(1.0000001 as f32, t, t + t, t * (3.0 as f32), t * (4.0 as f32)).to_string());
  print(lanes(t, t, t).to_string());
  return 0;
}
`

// Each native backend gives __fma_f32 one fused instruction in single
// precision, and f32x4.mul_add one fused lane instruction. Wasm calls
// the software form of the runtime.
func TestSelfHostFmaF32Emission(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(fma32PolySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, prefix, end, fused, lane, separate string
		lanes                                      int
	}{
		{e2eharness.TargetX86_64Linux, "__fn_", ".cfi_endproc", "vfmadd231ss", "ps %", "mulss", 1},
		{e2eharness.TargetArm64Linux, "__fn_", ".cfi_endproc", "fmadd s", "fmla ", "fmul", 1},
		{e2eharness.TargetWasm32Wasi, "(func $", "(func ", "call $__fern_fma_f32", "call $__fern_fma_f32", "f32x4.mul", 4},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), c.target, src)
			suffix := ":"
			if c.prefix == "(func $" {
				suffix = " "
			}
			poly := fmaSection(t, asm, c.prefix+"poly32"+suffix, c.end)
			if n := strings.Count(poly, c.fused); n != 6 {
				t.Fatalf("poly32 has %d of %q, want 6:\n%s", n, c.fused, poly)
			}
			lanes := fmaSection(t, asm, c.prefix+"lanes"+suffix, c.end)
			fusedLanes := 0
			for _, ln := range strings.Split(lanes, "\n") {
				if strings.Contains(ln, "fm") && strings.Contains(ln, c.lane) {
					fusedLanes++
				}
			}
			if n := fusedLanes; n != c.lanes {
				t.Fatalf("lanes has %d of %q, want %d:\n%s", n, c.lane, c.lanes, lanes)
			}
			if strings.Contains(poly+lanes, c.separate) {
				t.Fatalf("a product is rounded with %q:\n%s\n%s", c.separate, poly, lanes)
			}
		})
	}
}

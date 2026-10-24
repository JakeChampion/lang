package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func blockExprInterpExit(t *testing.T, src string) (int, string, string) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

func TestBlockExprInterp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			"if-block-tail",
			`function main(): i32 {
				let e = 5;
				let x: i32 = if (e > 0) { let k = e + 1; k } else { 0 };
				return x;
			}`,
			6,
		},
		{
			"if-block-else-taken",
			`function main(): i32 {
				let e = 0;
				let x: i32 = if (e > 0) { let k = e + 1; k } else { let z = 9; z };
				return x;
			}`,
			9,
		},
		{
			"match-arm-block",
			`function main(): i32 {
				let tag = 0;
				let r: i32 = match (tag) { 0 => { let s = tag + 5; s }, _ => 99 };
				return r;
			}`,
			5,
		},
		{
			"multi-leading-statements",
			`function main(): i32 {
				let x: i32 = if (true) { let a = 2; let b = a * 3; let c = b + 1; c } else { 0 };
				return x;
			}`,
			7,
		},
		{
			"compose-through-match",
			`function main(): i32 {
				let c = true;
				let r: i32 = match (if (c) { let k = 7; k } else { 0 }) {
					7 => { let hit = 1; hit },
					_ => 0
				};
				return r;
			}`,
			1,
		},
		{
			"locals-confined-to-block",
			`function main(): i32 {
				let a: i32 = if (true) { let k = 10; k } else { 0 };
				let b: i32 = if (true) { let k = 20; k } else { 0 };
				return a + b;
			}`,
			30,
		},
		{
			"value-position-var-rhs",
			`function main(): i32 {
				let n: i32 = { let k = 3; let m = 4; k * m };
				return n;
			}`,
			12,
		},
		{
			"value-position-call-arg",
			`function id(x: i32): i32 { return x; }
			function main(): i32 {
				return id({ let a = 40; a + 2 });
			}`,
			42,
		},
		{
			"value-position-single-expr",
			`function main(): i32 {
				let n: i32 = { 7 };
				return n + 1;
			}`,
			8,
		},
		{
			"cf-conditional-return-tail",
			`function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
			function main(): i32 { return f(5); }`,
			6,
		},
		{
			"cf-conditional-return-taken",
			`function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
			function main(): i32 { return f(-1); }`,
			99,
		},
		{
			"cf-break-in-block",
			`function main(): i32 {
				let s: i32 = 0; let i: i32 = 0;
				while (i < 10) { i = i + 1; let d: i32 = { if (i == 4) { break; } i }; s = s + d; }
				return s;
			}`,
			6,
		},
		{
			"cf-continue-in-block",
			`function main(): i32 {
				let s: i32 = 0; let i: i32 = 0;
				while (i < 6) { i = i + 1; let d: i32 = { if (i == 3) { continue; } i }; s = s + d; }
				return s;
			}`,
			18,
		},
		{
			"cf-no-tail-all-return-first",
			`function f(n: i32): i32 { let x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
			function main(): i32 { return f(-1); }`,
			1,
		},
		{
			"cf-no-tail-all-return-second",
			`function f(n: i32): i32 { let x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
			function main(): i32 { return f(5); }`,
			2,
		},
		{
			"cf-if-expr-both-arms-diverge",
			`function f(n: i32): i32 { let x: i32 = if (n < 0) { return 1; } else { return 2; }; return x; }
			function main(): i32 { return f(5); }`,
			2,
		},
		{
			"cf-match-arm-diverges",
			`function f(n: i32): i32 { let x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } }; return x; }
			function main(): i32 { return f(0); }`,
			100,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := blockExprInterpExit(t, c.src)
			if code != c.want {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, c.want, out, errb)
			}
		})
	}
}

func TestBlockExprCompiledIfBranch(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let e = 5;
	let x: i32 = if (e > 0) { let k = e + 1; k } else { 0 };
	print("x=" + x.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "x=6"))
}

func TestBlockExprCompiledValuePosition(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let n: i32 = { let k = 3; let m = 4; k * m };
	let s: string = { let a = "foo"; let b = "bar"; a + b };
	print("n=" + n.to_string() + " s=" + s);
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "n=12 s=foobar"))
}

func TestBlockExprCompiledControlFlow(t *testing.T) {
	src := `import "std/i32";
function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 {
	let s: i32 = 0; let i: i32 = 0;
	while (i < 10) { i = i + 1; let d: i32 = { if (i == 4) { break; } i }; s = s + d; }
	print("a=" + f(5).to_string() + " b=" + f(-1).to_string() + " s=" + s.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "a=6 b=99 s=6"))
}

func TestBlockExprCompiledControlFlowNoTail(t *testing.T) {
	src := `import "std/i32";
function f(n: i32): i32 { let x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
function g(n: i32): i32 { let x: i32 = if (n < 0) { return 10; } else { return 20; }; return x; }
function h(n: i32): i32 { let x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } }; return x; }
function main(): i32 {
	print("f-1=" + f(-1).to_string() + " f5=" + f(5).to_string() +
		" g-1=" + g(-1).to_string() + " g5=" + g(5).to_string() +
		" h0=" + h(0).to_string() + " h3=" + h(3).to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "f-1=1 f5=2 g-1=10 g5=20 h0=100 h3=6"))
}

func TestBlockExprCompiledMatchArm(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let t = 0;
	let r: i32 = match (t) { 0 => { let v = t + 10; v * 2 }, _ => 0 };
	print("r=" + r.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "r=20"))
}

func TestBlockExprCompiledStringTail(t *testing.T) {
	src := `function main(): i32 {
	let a = "foo";
	let b = "bar";
	let s: string = if (true) { let joined = a + b; joined } else { "" };
	print("s=" + s);
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "s=foobar"))
}

func TestBlockExprStringTailRuntimeCondition(t *testing.T) {
	src := `function pick(n: i32): string {
	let s: string = if (n > 1) { let joined = "foo" + "bar"; joined } else { "" };
	return s;
}
function main(): i32 {
	let i: i32 = 0;
	while (i < 3) {
		print("s=" + pick(i));
		i = i + 1;
	}
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "s=\ns=\ns=foobar"))
}

func TestBlockExprCompiledNestedIfTail(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let x: i32 = if (true) {
		let base = 3;
		if (base > 0) { base * 7 } else { 0 - 1 }
	} else { 0 };
	print("x=" + x.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "x=21"))
}

func TestBlockExprCompiledNestedMatchTail(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let sel = 1;
	let r: i32 = if (true) {
		let bump = 100;
		match (sel) {
			1 => { let hit = bump + 5; hit },
			_ => 0
		}
	} else { 0 };
	print("r=" + r.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "r=105"))
}

func TestBlockExprCompiledVoidLambdaBranchTail(t *testing.T) {
	src := `import "std/i32";
enum M { A(i32), B(string), C }
function each(xs: M[], f: (M) => void): void {
	for x in xs {
		f(x);
	}
}
function main(): i32 {
	each([A(7), B("x"), C], (m: M): void => {
		match (m) {
			A(n) => {
				print("a=" + n.to_string());
			},
			B(s) => {
				print("b=" + s);
			},
			C => {
				return;
			}
		}
	});
	let sign: (i32) => void = (n: i32): void => {
		if (n > 0) {
			print("pos");
		} else {
			print("neg");
		}
	};
	sign(3);
	sign(-3);
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "a=7\nb=x\npos\nneg"))
}

func TestBlockExprCompiledLocalsConfined(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	let a: i32 = if (true) { let k = 10; k } else { 0 };
	let b: i32 = if (true) { let k = 20; k } else { 0 };
	print("sum=" + (a + b).to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "sum=30"))
}

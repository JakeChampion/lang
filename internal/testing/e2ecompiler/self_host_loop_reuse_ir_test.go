package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var loopReuseIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"loop-struct-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`,
		40},
	{"loop-struct-donor-live",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let b: P = P { x: i, y: 3 }; sum = sum + a.x + a.y + b.x + b.y; i = i + 1; } return sum; }`,
		34},
	{"loop-tuple-reuse",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34},
	{"loop-tuple-donor-live",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`,
		34},
	{"loop-struct-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; i = i + 1; } return sum; }`,
		0},
	{"loop-funcupdate-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: P = P { x: i, y: 0 }; let c: P = P { ...d, y: 3 }; sum = sum + c.x + c.y; i = i + 1; } return sum; }`,
		18},
	{"loop-funcupdate-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let d: P = P { x: i, y: 0 }; let c: P = P { ...d, y: 3 }; sum = (sum + c.x + c.y) % 1000; i = i + 1; } return sum; }`,
		0},
	{"if-arm-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { let cond: i32 = 1; let r: i32 = 0; if (cond > 0) { let a: P = P { x: 10 * cond, y: 20 }; let s: i32 = a.x + a.y; let b: P = P { x: 3 * cond, y: 4 }; r = s + b.x + b.y; } return r; }`,
		37},
	{"if-in-loop-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { if (i > 0) { let a: P = P { x: i, y: 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i, y: 2 }; sum = (sum + s + b.x + b.y) % 1000; } i = i + 1; } return sum; }`,
		229},
	{"loop-escaping-recipient-struct",
		`struct P { x: i32, y: i32 } function main(): i32 { let acc: P[] = []; let i: i32 = 0; while (i < 5) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; let b: P = P { x: i, y: 100 }; acc = acc.append(b); i = i + 1; } let sum: i32 = 0; let j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].x + acc[j].y; j = j + 1; } return sum; }`,
		254},
	{"loop-escaping-recipient-tuple",
		`function main(): i32 { let acc: (i32, i32)[] = []; let i: i32 = 0; while (i < 5) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; let b: (i32, i32) = (i, 100); acc = acc.append(b); i = i + 1; } let sum: i32 = 0; let j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].0 + acc[j].1; j = j + 1; } return sum; }`,
		254},
	{"loop-struct-array-field-reuse",
		`struct P { xs: i32[], n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { xs: [1, 2, 3], n: i }; let s: i32 = a.n + a.xs[0]; let b: P = P { xs: [4, 5], n: i * 2 }; sum = sum + s + b.xs[1] + b.n; i = i + 1; } return sum; }`,
		42},
	{"cross-block-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: i + 1 }; let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + s; i = i + 1; } return sum; }`,
		31},
	{"cross-block-both-arms",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: 1 }; if (i % 2 == 0) { let b: P = P { x: i, y: 10 }; sum = sum + b.x + b.y; } else { let c: P = P { x: i, y: 20 }; sum = sum + c.x + c.y; } i = i + 1; } return sum; }`,
		66},
	{"cross-block-both-arms-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: P = P { x: i, y: 1 }; if (i % 2 == 0) { let b: P = P { x: i, y: 10 }; sum = (sum + b.x + b.y) % 113; } else { let c: P = P { x: i, y: 20 }; sum = (sum + c.x + c.y) % 113; } i = i + 1; } return sum; }`,
		53},
	{"cross-block-donor-used-after",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: P = P { x: i, y: 1 }; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = sum + b.x + b.y; } sum = sum + a.x + a.y; i = i + 1; } return sum; }`,
		25},
	{"cross-block-escaping-recipient",
		`struct P { x: i32, y: i32 } function main(): i32 { let acc: P[] = []; let i: i32 = 0; while (i < 6) { let a: P = P { x: i, y: 1 }; let s: i32 = a.x + a.y; if (i > 2) { let b: P = P { x: i, y: 100 }; acc = acc.append(b); } i = i + 1; } let sum: i32 = 0; let j: i32 = 0; while (j < acc.len()) { sum = sum + acc[j].x + acc[j].y; j = j + 1; } return sum; }`,
		56},
	{"cross-block-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: P = P { x: i, y: 1 }; let s: i32 = a.x + a.y; if (i > 0) { let b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; } i = i + 1; } return sum; }`,
		229},
	{"cross-block-tuple-reuse",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let a: (i32, i32) = (i, i + 1); let s: i32 = a.0 + a.1; if (i > 0) { let b: (i32, i32) = (i, 3); sum = sum + b.0 + b.1; } sum = sum + s; i = i + 1; } return sum; }`,
		31},
	{"cross-block-tuple-churn-safe",
		`function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let a: (i32, i32) = (i, 1); let s: i32 = a.0 + a.1; if (i > 0) { let b: (i32, i32) = (i, 3); sum = (sum + b.0 + b.1) % 1000; } i = i + 1; } return sum; }`,
		229},
	{"loop-nested-struct-field-reuse",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let s: i32 = d.inner.a + d.inner.b + d.n; let c: Outer = Outer { inner: Inner { a: i * 2, b: 3 }, n: 5 }; sum = sum + s + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		66},
	{"loop-nested-struct-field-donor-live",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let c: Outer = Outer { inner: Inner { a: i * 2, b: 3 }, n: 5 }; sum = sum + d.inner.a + d.inner.b + d.n + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		66},
	{"loop-nested-struct-field-churn-safe",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let s: i32 = d.inner.a + d.inner.b + d.n; let c: Outer = Outer { inner: Inner { a: i, b: 3 }, n: 5 }; sum = (sum + s + c.inner.a + c.inner.b + c.n) % 1000; i = i + 1; } return sum; }`,
		0},
	{"loop-funcupdate-nested-struct-reuse",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let c: Outer = Outer { ...d, inner: Inner { a: i * 2, b: 3 } }; sum = sum + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		30},
	{"loop-funcupdate-nested-struct-donor-live",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let c: Outer = Outer { ...d, inner: Inner { a: i * 2, b: 3 } }; sum = sum + d.inner.a + d.inner.b + c.inner.a + c.inner.b + c.n; i = i + 1; } return sum; }`,
		46},
	{"loop-funcupdate-nested-struct-churn-safe",
		`struct Inner { a: i32, b: i32 } struct Outer { inner: Inner, n: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let d: Outer = Outer { inner: Inner { a: i, b: i + 1 }, n: i }; let c: Outer = Outer { ...d, inner: Inner { a: i, b: 3 } }; sum = (sum + c.inner.a + c.inner.b + c.n) % 1000; i = i + 1; } return sum; }`,
		0},
	{"cross-block-enum-field-reuse",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: M = M { tag: i, st: On(i + 1) }; let s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } if (i > 0) { let b: M = M { tag: i, st: On(3) }; let r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = sum + r; } sum = sum + s; i = i + 1; } return sum; }`,
		31},
	{"cross-block-enum-field-donor-live",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 4) { let d: M = M { tag: i, st: On(i + 1) }; if (i > 0) { let b: M = M { tag: i, st: On(3) }; let r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = sum + r; } let s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } sum = sum + s; i = i + 1; } return sum; }`,
		31},
	{"cross-block-enum-field-churn-safe",
		`enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5000000) { let d: M = M { tag: i, st: On(i + 1) }; let s: i32 = 0; match (d.st) { On(v) => { s = v + d.tag; }, Off => { s = d.tag; } } if (i > 0) { let b: M = M { tag: i, st: On(3) }; let r: i32 = 0; match (b.st) { On(v) => { r = v + b.tag; }, Off => { r = 0; } } sum = (sum + r) % 1000; } sum = (sum + s) % 1000; i = i + 1; } return sum; }`,
		229},
}

func TestSelfHostLoopReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range loopReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

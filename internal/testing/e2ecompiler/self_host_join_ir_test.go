package e2ecompiler

import (
	"testing"
)

func TestSelfHostJoinIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		body string
	}{
		{
			"multi",
			`function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("bb"); xs = xs.append("ccc"); return xs.join("-").len(); }`,
		},
		{
			"empty",
			`function main(): i32 { let xs: string[] = []; return xs.join(",").len(); }`,
		},
		{
			"single",
			`function main(): i32 { let xs: string[] = []; xs = xs.append("solo"); return xs.join(",").len(); }`,
		},
		{
			"empty-sep",
			`function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); xs = xs.append("c"); return xs.join("").len(); }`,
		},
		{
			"multi-char-sep",
			`function main(): i32 { let xs: string[] = []; xs = xs.append("x"); xs = xs.append("y"); xs = xs.append("z"); return xs.join(" - ").len(); }`,
		},
		{
			"concat-result",
			`function main(): i32 { let xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); let s: string = "[" + xs.join(",") + "]"; return s.len(); }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := runInterpExit(t, "import \"std/array\";\n"+tc.body)
			got := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "join_"+tc.name, tc.body)
			if got != want {
				t.Errorf("%s: self-host IR exited %d, native oracle %d", tc.name, got, want)
			}
		})
	}
}

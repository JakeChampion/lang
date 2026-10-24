package e2ecompiler

import (
	"testing"
)

const copyingBuiltinProlog = `function round(n: i32): i32 {
    let out: string = "";
    let i: i32 = 0;
    while (i < 3) { out = out + "abcdefgh"; i = i + 1; }
`

const copyingBuiltinEpilog = `
    return out.len() + n;
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 50) { t = t + round(k); k = k + 1; }
    return t % 7;
}
`

type copyingBuiltinCase struct {
	name      string
	decls     string
	use       string
	pinCounts bool
}

func copyingBuiltinCases() []copyingBuiltinCase {
	return []copyingBuiltinCase{
		{name: "control_len", use: `let q: i32 = out.len();`, pinCounts: true},
		{name: "print", use: `print(out);`, pinCounts: true},
		{name: "eprint", use: `eprint(out);`, pinCounts: true},
		{name: "memchr", use: `let q: i32 = __memchr(out, 10, 0);`, pinCounts: true},
		{name: "count_byte", use: `let q: i32 = __count_byte(out, 97);`, pinCounts: true},
		{name: "scan_set", use: `let st: u8[] = __alloc_u8(256);
    let q: i32 = __scan_set(out, 0, st);`, pinCounts: true},
		{name: "mismatch", use: `let q: i32 = __mismatch(out, 0, out, 0, 24);`, pinCounts: true},
		{name: "writer_write", use: `let w: Writer = stdout();
    match (w.write(out)) { Err(_) => { return -1; }, Ok(_) => {} }`, pinCounts: true},
		{
			name: "user_write_method_retains",
			decls: `struct Sink { last: string }
function (k: Sink) write(s: string): Sink { return Sink { last: s }; }
`,
			use: `let w: Sink = Sink { last: "" };
    w = w.write(out);
    if (w.last.len() != out.len() || w.last[0] != b'a') { return 250; }`,
		},
	}
}

func TestSelfHostCopyingBuiltinArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range copyingBuiltinCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decls + copyingBuiltinProlog + "    " + tc.use + copyingBuiltinEpilog
			asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "copying_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != 3 {
				t.Fatalf("%s exited %d, want 3 (99 = rc underflow; 139 = it read freed memory; 250 = the retained string changed)", tc.name, exit)
			}
			if !tc.pinCounts {
				return
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if frees < 100 {
				t.Errorf("%s: %s — the accumulator's blocks were not freed; the credit did not reach `out`", tc.name, summary)
			}
			if allocs != frees || live != 0 {
				t.Errorf("%s: %s — want every block freed, none unpaired", tc.name, summary)
			}
		})
	}
}

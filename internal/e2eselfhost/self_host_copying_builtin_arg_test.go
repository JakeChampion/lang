package e2eselfhost

import (
	"testing"
)

// --- A string local passed to a copying builtin keeps its reclaim ----------
//
// Native credits the builtins that copy a string argument rather than retain
// it — `copyingBuiltinArgs` in rc_analysis.go (#7867, #8394) — so `print(out)`
// or `w.write(out)` leaves `out` owned and its scope-exit release freeing. The
// self-host's escape walk had no such credit: any builtin taking `out` was
// an escape, so the local lost its release and every round leaked its
// accumulator. copying_builtin_keys (irlower.fern) seeds the same table into
// the borrowability registry both builders produce.
//
// The method form is admitted by NAME: the walk cannot see a receiver's type,
// so "Writer.write" is seeded only while no user receiver method is called
// `write`. The last case declares one that retains its argument into a field
// and checks the program still reads the value back: with the credit wrongly
// reaching it, `out` would be released under the field (exit 139, or 250 when
// the bytes had already been recycled).
//
// `print` and `Writer.write` each leave one block per call on the self-host
// whatever their argument — print's newline-joined temp and write's
// Option[IoError] result box, measured identically with a string literal
// (#8410). Those rows pin exactly that one block, so the accumulator's own
// three concats per round are proven freed and the row moves when the gap
// closes.

const copyingBuiltinProlog = `function round(n: i32): i32 {
    var out: string = "";
    var i: i32 = 0;
    while (i < 3) { out = out + "abcdefgh"; i = i + 1; }
`

const copyingBuiltinEpilog = `
    return out.len() + n;
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 50) { t = t + round(k); k = k + 1; }
    return t % 7;
}
`

type copyingBuiltinCase struct {
	name  string
	decls string
	use   string
	// leakPerRound is the number of blocks the builtin itself leaves behind
	// per call (#8410); the accumulator's blocks must be freed regardless.
	// The retaining user method is checked by its exit code only, since its
	// Sink's own drop may legitimately free the string once.
	pinCounts    bool
	leakPerRound int64
}

func copyingBuiltinCases() []copyingBuiltinCase {
	return []copyingBuiltinCase{
		{name: "control_len", use: `var q: i32 = out.len();`, pinCounts: true},
		{name: "print", use: `print(out);`, pinCounts: true, leakPerRound: 1},
		{name: "eprint", use: `eprint(out);`, pinCounts: true},
		{name: "memchr", use: `var q: i32 = __memchr(out, 10, 0);`, pinCounts: true},
		{name: "count_byte", use: `var q: i32 = __count_byte(out, 97);`, pinCounts: true},
		{name: "writer_write", use: `var w: Writer = stdout();
    match (w.write(out)) { Some(_) => { return -1; }, None => {} }`, pinCounts: true, leakPerRound: 1},
		{
			name: "user_write_method_retains",
			decls: `struct Sink { last: string }
function (k: Sink) write(s: string): Sink { return Sink { last: s }; }
`,
			use: `var w: Sink = Sink { last: "" };
    w = w.write(out);
    if (w.last.len() != out.len() || w.last[0] != b'a') { return 250; }`,
		},
	}
}

func TestSelfHostCopyingBuiltinArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range copyingBuiltinCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decls + copyingBuiltinProlog + "    " + tc.use + copyingBuiltinEpilog
			asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "copying_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			// 50 rounds of 24 + n: 2425 % 7.
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
			// 50 rounds, three concats each: every accumulator block is freed.
			if frees < 150 {
				t.Errorf("%s: %s — the accumulator's blocks were not freed; the credit did not reach `out`", tc.name, summary)
			}
			if allocs-frees != tc.leakPerRound*50 || (tc.leakPerRound == 0 && live != 0) {
				t.Errorf("%s: %s — want exactly %d unpaired block(s) per round", tc.name, summary, tc.leakPerRound)
			}
		})
	}
}

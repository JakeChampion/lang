package e2e

import (
	"testing"
)

// Regression tests for two bugs surfaced while writing the
// examples/cli/ tools (the CLI command-line utilities).
//
// Bug 1 — `break` inside a `match` arm inside a loop was a no-op on
// every codegen backend (x86-64 / arm64 / wasm), while the interpreter
// did the right thing. The `match` lowering pushed its own end-label
// onto the shared break stack, so a user `break` exited the match
// instead of the enclosing loop — turning `while (true) { match … {
// … => break } }` into an infinite loop. (`continue` was unaffected;
// it rides a separate stack the match doesn't touch.)
//
// Bug 2 — `(w: Writer).write(s)` on x86-64 corrupted the buffer
// pointer for SSO (small-string-optimized, <=7 byte) strings: the
// runtime treated the inline-packed register value as a heap address
// and the write(2) syscall faulted EFAULT. String constants happened
// to fold to heap form and masked it; any runtime-built short string
// (e.g. a stdin chunk) tripped it.

// --- Bug 1: break / continue inside a match arm ---

// `break` at v==3 must exit the WHILE loop. If break only fell out of
// the match (the bug), `count` would keep incrementing to 100.
const matchBreakSrc = `function main(): i32 {
    var count: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var o: Option[i32] = Some(i);
        match (o) {
            Some(v) => {
                if (v == 3) { break; }
                count = count + 1;
            },
            None => {},
        }
        i = i + 1;
    }
    return count;
}`

// `continue` skips even values; only odds in 1..=10 are counted (5).
// A broken continue (falling out of the match) would count all 10.
const matchContinueSrc = `function main(): i32 {
    var odds: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        i = i + 1;
        var o: Option[i32] = Some(i);
        match (o) {
            Some(v) => {
                if (v - v / 2 * 2 == 0) { continue; }
                odds = odds + 1;
            },
            None => {},
        }
    }
    return odds;
}`

var matchBreakCases = []struct {
	name string
	src  string
	want int
}{
	{"break_exits_loop", matchBreakSrc, 3},
	{"continue_skips_in_loop", matchContinueSrc, 5},
}

func TestX86_64MatchBreakContinue(t *testing.T) {
	for _, c := range matchBreakCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d (break/continue inside match arm must target the loop)", code, c.want)
			}
		})
	}
}

func TestArm64MatchBreakContinue(t *testing.T) {
	for _, c := range matchBreakCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d (break/continue inside match arm must target the loop)", code, c.want)
			}
		})
	}
}

func TestWASMMatchBreakContinue(t *testing.T) {
	for _, c := range matchBreakCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("got %d, want %d (break/continue inside match arm must target the loop)", got, c.want)
			}
		})
	}
}

// --- Bug 2: Writer.write with an SSO (short) string ---

// Writes a 3-byte SSO string then a >7-byte heap string to the stdout
// Writer. `rc` stays 0 only if BOTH writes report success (None); the
// pre-fix x86-64 backend returned Some(EFAULT) for the SSO write. The
// stdout content is checked too.
const writerSSOSrc = `function main(): i32 {
    var w: Writer = stdout();
    var rc: i32 = 0;
    match (w.write("hi\n")) { Some(_) => { rc = 1; }, None => {}, }
    match (w.write("longer than seven\n")) { Some(_) => { rc = rc + 2; }, None => {}, }
    return rc;
}`

const writerSSOWant = "hi\nlonger than seven\n"

func TestX86_64WriterWriteSSO(t *testing.T) {
	out, code := compileAndRunX86_64(t, writerSSOSrc)
	if code != 0 {
		t.Errorf("Writer.write returned an error (rc=%d); SSO short-string write should succeed", code)
	}
	if out != writerSSOWant {
		t.Errorf("stdout = %q, want %q", out, writerSSOWant)
	}
}

func TestArm64WriterWriteSSO(t *testing.T) {
	out, code := compileAndRunArm64(t, writerSSOSrc)
	if code != 0 {
		t.Errorf("Writer.write returned an error (rc=%d)", code)
	}
	if out != writerSSOWant {
		t.Errorf("stdout = %q, want %q", out, writerSSOWant)
	}
}

// NB: no WASM counterpart for the Writer.write SSO test. The wasm
// backend has a SEPARATE, pre-existing bug in the `Writer.write`
// METHOD path (buildWriterWriteBody) that corrupts a byte near the
// end of a heap-string write — independent of the x86-64 SSO fix here
// and tracked as its own follow-up. The wasm `write` / `print` /
// `write_file` builtins (which the examples/cli/ tools actually use)
// handle long strings correctly, so this gap doesn't affect them.

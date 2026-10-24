package e2e

import (
	"testing"
)

const matchBreakSrc = `function main(): i32 {
    let count: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let o: Option[i32] = Some(i);
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

const matchContinueSrc = `function main(): i32 {
    let odds: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        i = i + 1;
        let o: Option[i32] = Some(i);
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

const writerSSOSrc = `function main(): i32 {
    let w: Writer = stdout();
    let rc: i32 = 0;
    match (w.write("hi\n")) { Err(_) => { rc = 1; }, Ok(_) => {}, }
    match (w.write("longer than seven\n")) { Err(_) => { rc = rc + 2; }, Ok(_) => {}, }
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

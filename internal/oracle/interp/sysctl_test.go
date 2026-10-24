package interp

import (
	"runtime"
	"testing"
)

func TestSysctlTakesItsMib(t *testing.T) {
	src := `function main(): i32 {
    match (sysctl([1, 1])) {
        Ok(b) => { if (b.len() > 0) { return 10; } return 11; },
        Err(e) => {
            match (e) {
                Other(_, _, n) => { return n; },
                _ => { return 12; }
            }
        }
    }
}`
	want := "38"
	if runtime.GOOS == "darwin" {
		want = "10"
	}
	if val, _ := runCapture(t, src); val != want {
		t.Errorf("sysctl([1, 1]) program = %s, want %s", val, want)
	}
}

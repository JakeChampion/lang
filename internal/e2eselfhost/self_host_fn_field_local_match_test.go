package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// A `match` over a call through a fn-typed local bound from a struct field
// (`var next = producer.next; match (next(i))`) lowers on the IR path.
//
// The scrutinee's Option payload is named from the fn value's recorded return.
// That sidecar was seeded from a lambda, a named function or a `__mkclo$`
// initialiser only, so a field-read initialiser left the local without one and
// the match bailed the whole function — while the direct `match
// (producer.next(i))` form, which reads the field's declared return, lowered.
// std/http's `__chunks_joined` and std/tcp's `__tail_piece` are both this
// shape. The program counts the bytes of the chunks a ChunkProducer answers:
// "ab" then "cde" then None, so it exits 5.
func TestSelfHostMatchOnCallThroughFnFieldLocal(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	src := `function total(producer: ChunkProducer): i32 {
    var next: (i32) => Option[u8[]] = producer.next;
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        match (next(i)) {
            Some(chunk) => { n = n + chunk.len(); },
            None => { return n; }
        }
        i = i + 1;
    }
    return n;
}
function main(): i32 {
    var p: ChunkProducer = ChunkProducer { next: (i: i32): Option[u8[]] => {
        if (i == 0) { return Some("ab".bytes()); }
        if (i == 1) { return Some("cde".bytes()); }
        return None;
    } };
    return total(p);
}
`
	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if !bytes.Contains([]byte(asm), []byte(".Lssa_")) {
		t.Fatal("the program did not route through the IR path")
	}
	bin := buildBin(t, gcc, progDir, "fn_field_local_match", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 5 {
		t.Fatalf("exited %d, want 5", code)
	}
}

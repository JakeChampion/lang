package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestSelfHostMatchOnCallThroughFnFieldLocal(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	src := `function total(producer: ChunkProducer): i32 {
    let next: (i32) => Option[u8[]] = producer.next;
    let n: i32 = 0;
    let i: i32 = 0;
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
    let p: ChunkProducer = ChunkProducer { next: (i: i32): Option[u8[]] => {
        if (i == 0) { return Some("ab".bytes()); }
        if (i == 1) { return Some("cde".bytes()); }
        return None;
    }, idle_ms: 0, filler: [], trailers: (): HeaderMap => HeaderMap { names: [], values: [] } };
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

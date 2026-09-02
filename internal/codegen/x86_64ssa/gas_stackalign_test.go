package x86_64ssa

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// rspDeltaAtCalls walks one emitted function's text and returns the stack
// pointer's displacement, in bytes, at each `call` it reaches. The walk starts
// after the prologue, where rsp is 16-aligned: the return address pushed by the
// caller leaves rsp 8 mod 16 at entry, `push rbp` brings it back to 0, and the
// frame `sub` is 16-aligned by construction.
//
// This reads the OUTPUT rather than re-deriving what the emitter intended, so
// it disagrees with a wrong pad instead of agreeing with it.
func rspDeltaAtCalls(t *testing.T, asm, label string) []int {
	t.Helper()
	lines := strings.Split(asm, "\n")
	i := 0
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == label+":" {
			break
		}
	}
	if i == len(lines) {
		t.Fatalf("no %q in the emitted program", label)
	}
	delta, inPrologue := 0, label != "_start"
	var out []int
	for i++; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		// A label at the outer level ends this function; block labels (.L…) do not.
		if strings.HasSuffix(ln, ":") && !strings.HasPrefix(ln, ".L") {
			break
		}
		switch {
		case inPrologue && ln == "push rbp":
			// the entry-to-aligned step, not a body push
		case inPrologue && strings.HasPrefix(ln, "sub rsp,"):
			inPrologue = false // the frame is 16-aligned by construction
		case ln == "mov rbp, rsp":
		case strings.HasPrefix(ln, "push "):
			inPrologue = false
			delta -= 8
		case strings.HasPrefix(ln, "pop "):
			delta += 8
		case strings.HasPrefix(ln, "sub rsp, "), strings.HasPrefix(ln, "add rsp, "):
			n, err := strconv.Atoi(strings.TrimSpace(ln[len("sub rsp, "):]))
			if err != nil {
				t.Fatalf("unparsed stack adjust %q", ln)
			}
			if strings.HasPrefix(ln, "sub") {
				delta -= n
			} else {
				delta += n
			}
		case strings.HasPrefix(ln, "call "):
			out = append(out, delta)
		}
	}
	return out
}

// Every call must execute with rsp 16-byte aligned — System V requires it, and
// nothing in a small integer program faults when it is wrong, so it needs its
// own gate rather than riding on the run tests (#8087).
func TestStackArgsKeepCallsAligned(t *testing.T) {
	for _, n := range []int{6, 7, 8, 9, 12} {
		callee := weightedSum("callee", n)
		main := ssa.NewFunc("main")
		me := main.NewBlock()
		var args []ssa.Value
		for _, v := range countUp(n) {
			args = append(args, constOp(main, me, v))
		}
		t1 := callOp(main, me, "callee", args...)
		t2 := callOp(main, me, "callee", args...)
		main.SetRet(me, main.AddOp(me, ssa.OpAdd, t1, t2))
		funcs := map[string]*ssa.Func{"callee": callee, "main": main}

		for _, nAlloc := range []int{1, 2, 8} {
			asm, err := EmitAsmModule(funcs, "main", nAlloc, nil)
			if err != nil {
				t.Fatalf("n=%d nAlloc=%d: %v", n, nAlloc, err)
			}
			for _, lbl := range []string{"_start", fnLabel("main")} {
				deltas := rspDeltaAtCalls(t, asm, lbl)
				if len(deltas) == 0 {
					t.Fatalf("n=%d nAlloc=%d %s: no call found, so this checked nothing", n, nAlloc, lbl)
				}
				for _, d := range deltas {
					if d%16 != 0 {
						t.Errorf("n=%d nAlloc=%d %s: a call runs with rsp %d off the frame, %d mod 16",
							n, nAlloc, lbl, d, ((d%16)+16)%16)
					}
				}
			}
		}
	}
}

// The same alignment rule through the closure path, where the env pointer is
// the callee's final argument and so pushes the count over the register half
// one argument earlier than a direct call does.
func TestStackArgsKeepIndirectCallsAligned(t *testing.T) {
	for _, n := range []int{5, 6, 7, 10} {
		target := weightedSum("target", n+1)
		apply := ssa.NewFunc("apply")
		ae := apply.NewBlock()
		c := makeClosureOp(apply, ae, "target")
		var args []ssa.Value
		for _, v := range countUp(n) {
			args = append(args, constOp(apply, ae, v))
		}
		apply.SetRet(ae, callIndirectOp(apply, ae, c, args...))
		funcs := map[string]*ssa.Func{"apply": apply, "target": target}

		for _, nAlloc := range []int{1, 2, 8} {
			asm, err := EmitAsmModule(funcs, "apply", nAlloc, nil)
			if err != nil {
				t.Fatalf("n=%d nAlloc=%d: %v", n, nAlloc, err)
			}
			deltas := rspDeltaAtCalls(t, asm, fnLabel("apply"))
			if len(deltas) == 0 {
				t.Fatalf("n=%d nAlloc=%d: no call found, so this checked nothing", n, nAlloc)
			}
			for _, d := range deltas {
				if d%16 != 0 {
					t.Errorf("n=%d nAlloc=%d: a call runs with rsp %d off the frame", n, nAlloc, d)
				}
			}
		}
	}
}

// _start pushes the entry's stack arguments too, from a 16-aligned process
// entry, so an odd count needs its own pad.
func TestEntryStackArgsKeepCallAligned(t *testing.T) {
	for _, n := range []int{6, 7, 8, 9, 12} {
		f := weightedSum("entry", n)
		funcs := map[string]*ssa.Func{"entry": f}
		asm, err := EmitAsmModule(funcs, "entry", 8, countUp(n))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		deltas := rspDeltaAtCalls(t, asm, "_start")
		if len(deltas) == 0 {
			t.Fatalf("n=%d: no call found in _start, so this checked nothing", n)
		}
		for _, d := range deltas {
			if d%16 != 0 {
				t.Errorf("n=%d: _start calls with rsp %d off process entry", n, d)
			}
		}
	}
}

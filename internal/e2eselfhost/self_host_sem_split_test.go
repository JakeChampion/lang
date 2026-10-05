package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tuple, record or variant built and only read is never allocated
// (seminline.split_constructions): each read takes the part off the
// construction, a phi joining constructions becomes a phi per part, and the
// unread construction is dropped. Each probe runs 100 rounds and prints its
// result times 1000 plus the heap allocations the rounds made; every round
// builds one heap string, so 100 is the floor. option and result join two
// variants with a phi, record and tuple hold the string beside a scalar, and
// held passes its record to a call, so that record is still a box a round.
// threaded carries a record through the loop by rebuilding it, building its
// string once: the rebuilds join in a phi, so no box is left at all.
const semSplitProgram = `struct Pair { name: string, n: i32 }
@noinline function ids(s: string): string { return s; }
@noinline function held(p: Pair): i32 { return p.name.len() + p.n; }
@noinline function option_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let s: string = ids("a") + "b";
        let o: Option[string] = None;
        if (i % 2 == 0) { o = Some(s); }
        match (o) { Some(x) => { t = t + x.len(); }, None => { t = t + 1; } }
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function result_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let s: string = ids("a") + "b";
        let r: Result[string, i32] = Err(i);
        if (i % 2 == 0) { r = Ok(s); }
        match (r) { Ok(x) => { t = t + x.len(); }, Err(e) => { t = t + e; } }
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function record_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let p: Pair = Pair { name: ids("a") + "b", n: i };
        t = t + p.name.len() + p.n;
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function tuple_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let p: (string, i32) = (ids("a") + "b", i);
        t = t + p.0.len() + p.1;
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function held_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let p: Pair = Pair { name: ids("a") + "b", n: i };
        t = t + held(p);
        i = i + 1;
    }
    return t * 1000 + ((__heap_alloc_count() - before) as i32);
}
@noinline function threaded_rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let p: Pair = Pair { name: ids("a") + "b", n: 0 };
    let i: i32 = 0;
    while (i < 100) {
        p = Pair { ...p, n: p.n + 1 };
        i = i + 1;
    }
    return (p.name.len() + p.n) * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(option_rounds()); print("");
    print_int(result_rounds()); print("");
    print_int(record_rounds()); print("");
    print_int(tuple_rounds()); print("");
    print_int(held_rounds()); print("");
    print_int(threaded_rounds()); print("");
    return 0;
}
`

var semSplitProduced = []string{"ids", "held", "option_rounds", "result_rounds", "record_rounds", "tuple_rounds", "held_rounds", "threaded_rounds"}

func TestSelfHostSemanticSplit(t *testing.T) {
	runSemanticProgram(t, "semsplit", semSplitProgram, semSplitProduced,
		semInlineWants("150100\n2600100\n5150100\n5150100\n5150200\n102001\n"))
}

// FERN_SEM_INLINE= turns the pass off, and every construction is a box again.
func TestSelfHostSemanticSplitOff(t *testing.T) {
	t.Setenv("FERN_SEM_INLINE", "")
	runSemanticProgram(t, "semsplit-off", semSplitProgram, semSplitProduced,
		semInlineWants("150150\n2600250\n5150200\n5150200\n5150200\n102002\n"))
}

// A function spliced into its one caller leaves no body behind, on any
// target, and the program still answers 51. A method is named outside the
// bodies (dispatch, drop), so one called once keeps its body and stays a call
// rather than being emitted twice.
const semSplicedProgram = `struct Acc { n: i32 }
function (self: Acc) bump(k: i32): Acc { return Acc { n: self.n + runtime(k) * 3 + 1 }; }
@noinline function runtime(n: i32): i32 { return n; }
function scaled(k: i32): (i32, i32) {
    let a: i32 = runtime(k) * 7;
    let b: i32 = a + runtime(3);
    if (a > b) { return (a, b); }
    return (b, a);
}
function main(): i32 {
    let p: (i32, i32) = scaled(5);
    let acc: Acc = Acc { n: runtime(1) };
    acc = acc.bump(p.0);
    return (acc.n + p.1) % 100;
}`

func TestSelfHostSemanticSplicedBodies(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(semSplicedProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, scaled, bump, call string }{
		{"x86-64-linux", "\n__fn_scaled:", "\n__fn_Acc__bump:", "call __fn_Acc__bump"},
		{"wasm32-wasi", "(func $scaled", "(func $Acc.bump", "call $Acc.bump"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			for _, off := range []bool{false, true} {
				var env []string
				if off {
					env = []string{"FERN_SEM_INLINE="}
				}
				out, err := os.ReadFile(cli.emit(t, src, tc.target, env...))
				if err != nil {
					t.Fatal(err)
				}
				asm := string(out)
				if got := strings.Contains(asm, tc.scaled); got != off {
					t.Errorf("FERN_SEM_INLINE off=%v: body of scaled emitted=%v, want %v", off, got, off)
				}
				if !strings.Contains(asm, tc.bump) || !strings.Contains(asm, tc.call) {
					t.Errorf("FERN_SEM_INLINE off=%v: Acc.bump is not a body called by main", off)
				}
			}
		})
	}
	if stderr, code := cli.exitOf(t, semSplicedProgram, "x86-64-linux"); code != 51 {
		t.Errorf("exited %d, want 51\n%s", code, stderr)
	}
}

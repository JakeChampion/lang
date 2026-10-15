package tv

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var kindOf = map[string]int{
	"const_i32": kConstI32, "const_i32_text": kConstI32Text, "const_i64": kConstI64,
	"int_extend": kIntExtend, "int_wrap": kIntWrap, "int_cast": kIntCast, "u32_wrap": kU32Wrap,
	"load_local": kLoadLocal, "store_local": kStoreLocal, "tee_local": kTeeLocal,
	"add": kAdd, "sub": kSub, "mul": kMul, "div_s": kDivS, "div_u": kDivU, "rem_s": kRemS, "rem_u": kRemU,
	"and": kAnd, "or": kOr, "xor": kXor, "shl": kShl, "shr_s": kShrS, "shr_u": kShrU, "not": kNot,
	"eq": kEq, "ne": kNe, "lt_s": kLtS, "le_s": kLeS, "gt_s": kGtS, "ge_s": kGeS,
	"lt_u": kLtU, "le_u": kLeU, "gt_u": kGtU, "ge_u": kGeU,
	"drop": kDrop, "call_direct": 149, "return": 160,
}

// dumpLine spells one op of a list like "load_local 0 ; const_i32 8 ; mul@64"
// the way ir.fern's tv_ops does. An `@64` suffix is the width; the argument is
// the i32 immediate for the integer-carrying ops and the string otherwise.
func dumpLine(t *testing.T, spec string) string {
	t.Helper()
	name, arg, _ := strings.Cut(strings.TrimSpace(spec), " ")
	width := 0
	if n, w, ok := strings.Cut(name, "@"); ok {
		name = n
		fmt.Sscan(w, &width)
	}
	kind, ok := kindOf[name]
	if !ok {
		t.Fatalf("no kind %q", name)
	}
	var i32, i64 int64
	str := ""
	switch kind {
	case kConstI32, kLoadLocal, kStoreLocal, kTeeLocal:
		fmt.Sscan(arg, &i32)
	case kConstI64:
		width = 64
		fmt.Sscan(arg, &i64)
	default:
		str = arg
	}
	return fmt.Sprintf("%s %d %d %d 0 %d 0 -1 :%s", name, kind, i32, i64, width, hex.EncodeToString([]byte(str)))
}

func ops(t *testing.T, list string) []Op {
	t.Helper()
	var out []Op
	if strings.TrimSpace(list) == "" {
		return out
	}
	for _, spec := range strings.Split(list, ";") {
		op, err := parseOp(dumpLine(t, spec))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, op)
	}
	return out
}

// verdict encodes and proves one pair: "unsat", "sat", "unknown", or the
// mismatch's reason.
func verdict(t *testing.T, z3, before, after string) string {
	t.Helper()
	q, err := Encode(Pair{Before: ops(t, before), After: ops(t, after)})
	var m *Mismatch
	if errors.As(err, &m) {
		return "mismatch: " + m.Reason
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := Prove(z3, []string{q})
	if err != nil {
		t.Fatalf("%q -> %q: %v\n%s", before, after, err, q)
	}
	return got[0]
}

// Every rewrite ir.fern's strength_rewrite makes, and the semantic edges the
// model has to get right for those proofs to mean anything.
func TestProvesSoundRewrites(t *testing.T) {
	z3 := Z3(t)
	for _, c := range []struct{ before, after string }{
		{"const_i32 1 ; mul", ""},
		{"const_i32 0 ; mul", "drop ; const_i32 0"},
		{"const_i32 8 ; mul", "const_i32 3 ; shl"},
		{"const_i32 1073741824 ; mul", "const_i32 30 ; shl"},
		{"const_i32 0 ; add", ""},
		{"const_i32 0 ; sub", ""},
		{"const_i32 0 ; or", ""},
		{"const_i32 0 ; xor", ""},
		{"const_i32 0 ; shl", ""},
		{"const_i32 0 ; shr_s", ""},
		{"const_i32 0 ; shr_u", ""},
		{"const_i32 0 ; and", "drop ; const_i32 0"},
		{"const_i32 -1 ; and", ""},
		{"const_i32 1 ; div_s", ""},
		{"const_i32 1 ; div_u", ""},
		{"const_i32 16 ; div_u", "const_i32 4 ; shr_u"},
		{"const_i32 1 ; rem_s", "drop ; const_i32 0"},
		{"const_i32 1 ; rem_u", "drop ; const_i32 0"},
		{"const_i32 8 ; rem_u", "const_i32 7 ; and"},
		// Division is total: x / 0 is 0, x % 0 is x, MIN / -1 wraps.
		{"const_i32 0 ; div_s", "drop ; const_i32 0"},
		{"const_i32 0 ; rem_u", ""},
		{"const_i32 -2147483648 ; const_i32 -1 ; div_s", "const_i32 -2147483648"},
		{"const_i32 -2147483648 ; const_i32 -1 ; rem_s", "const_i32 0"},
		// A shift count is masked to the width.
		{"const_i32 33 ; shl", "const_i32 1 ; shl"},
		{"const_i64 65 ; shr_s@64", "const_i64 1 ; shr_s@64"},
		{"const_i64 0 ; add@64", ""},
		{"const_i64 4 ; mul@64", "const_i64 2 ; shl@64"},
		{"const_i32 1 ; lt_u", "const_i32 0 ; eq"},
		{"int_cast u8 ; int_cast u8", "int_cast u8"},
		{"int_extend ; int_wrap", ""},
		{"store_local 3 ; load_local 3", "tee_local 3"},
	} {
		if got := verdict(t, z3, c.before, c.after); got != "unsat" {
			t.Errorf("%q -> %q: got %s, want unsat (proved)", c.before, c.after, got)
		}
	}
}

// The rewrites the pass must NOT make — strength_rewrite's comment says why
// for each — and the model's edges read the wrong way, each refuted.
func TestRefutesUnsoundRewrites(t *testing.T) {
	z3 := Z3(t)
	for _, c := range []struct{ before, after string }{
		// div_s rounds toward zero, shr_s toward negative infinity.
		{"const_i32 2 ; div_s", "const_i32 1 ; shr_s"},
		// rem_s keeps the dividend's sign; the mask does not.
		{"const_i32 4 ; rem_s", "const_i32 3 ; and"},
		{"const_i32 -8 ; mul", "const_i32 3 ; shl"},
		{"const_i32 16 ; div_u", "const_i32 3 ; shr_u"},
		{"const_i32 32 ; shl", "drop ; const_i32 0"},
		{"const_i32 0 ; div_u", ""},
		{"const_i32 1 ; lt_s", "const_i32 0 ; eq"},
		{"store_local 3 ; load_local 3", ""},
	} {
		if got := verdict(t, z3, c.before, c.after); got != "sat" {
			t.Errorf("%q -> %q: got %s, want sat (refuted)", c.before, c.after, got)
		}
	}
}

func TestCounterexampleNamesTheInput(t *testing.T) {
	z3 := Z3(t)
	q, err := Encode(Pair{Before: ops(t, "const_i32 2 ; div_s"), After: ops(t, "const_i32 1 ; shr_s")})
	if err != nil {
		t.Fatal(err)
	}
	model, err := Counterexample(z3, q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(model, "sat") || !strings.Contains(model, "s0") {
		t.Errorf("want a sat model naming s0, got:\n%s", model)
	}
}

// A pair that changes the stack's shape or a value's width is wrong before
// any solving.
func TestMismatches(t *testing.T) {
	for _, c := range []struct{ before, after, want string }{
		{"const_i32 0 ; mul", "const_i32 0", "the stack grows by 0 before and by 1 after"},
		{"const_i32 1 ; mul", "drop", "the stack grows by 0 before and by -1 after"},
		{"const_i32 0 ; add", "int_extend", "stack slot 0 is 32 bits before and 64 after"},
		{"const_i32 0 ; add", "const_i64 0 ; add@64", "s0 is read at 32 bits and at 64"},
	} {
		_, err := Encode(Pair{Before: ops(t, c.before), After: ops(t, c.after)})
		var m *Mismatch
		if !errors.As(err, &m) || m.Reason != c.want {
			t.Errorf("%q -> %q: got %v, want mismatch %q", c.before, c.after, err, c.want)
		}
	}
}

func dump(t *testing.T, pass, before, after string) string {
	t.Helper()
	side := func(name, list string) string {
		var lines []string
		if strings.TrimSpace(list) != "" {
			for _, spec := range strings.Split(list, ";") {
				lines = append(lines, dumpLine(t, spec))
			}
		}
		return fmt.Sprintf("fern-tv %s %d\n%s", name, len(lines), strings.Join(append(lines, ""), "\n"))
	}
	return "fern-tv " + pass + "\n" + side("before", before) + side("after", after) + "fern-tv end\n"
}

func TestParseSkipsOtherOutput(t *testing.T) {
	in := "warning: something\n" +
		dump(t, "strength", "load_local 0 ; const_i32 8 ; mul ; return", "load_local 0 ; const_i32 3 ; shl ; return") +
		"more noise\n" +
		dump(t, "strength", "", "")
	rws, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(rws) != 2 || rws[0].Pass != "strength" || len(rws[0].Before) != 4 || len(rws[1].After) != 0 {
		t.Fatalf("got %+v", rws)
	}
	if got := Render(rws[0].After); got != "load_local 0 ; const_i32 3 ; shl ; return" {
		t.Errorf("render: %q", got)
	}
}

func TestParseRejectsATruncatedBlock(t *testing.T) {
	in := strings.TrimSuffix(dump(t, "strength", "const_i32 1 ; mul", ""), "fern-tv end\n")
	if _, err := Parse(in); err == nil {
		t.Fatal("a block without its end line parsed")
	}
}

func TestSplitPairsRunsBetweenBarriers(t *testing.T) {
	rw := Rewrite{
		Before: ops(t, "load_local 0 ; const_i32 8 ; mul ; call_direct f ; load_local 1 ; return ; const_i32 1 ; mul"),
		After:  ops(t, "load_local 0 ; const_i32 3 ; shl ; call_direct f ; load_local 1 ; return"),
	}
	pairs, err := Split(rw)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || Render(pairs[0].After) != "load_local 0 ; const_i32 3 ; shl" || Render(pairs[1].Before) != "const_i32 1 ; mul" {
		t.Fatalf("got %+v", pairs)
	}
}

// The whole pipeline over one rewrite with a call between two sites: each run
// is proved on its own, with the call's result an input of the second.
func TestValidatesARewriteAcrossBarriers(t *testing.T) {
	z3 := Z3(t)
	rws, err := Parse(dump(t, "strength",
		"load_local 0 ; const_i32 16 ; div_u ; store_local 2 ; call_direct f ; const_i32 4 ; mul ; store_local 1 ; return",
		"load_local 0 ; const_i32 4 ; shr_u ; store_local 2 ; call_direct f ; const_i32 2 ; shl ; store_local 1 ; return"))
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := Split(rws[0])
	if err != nil {
		t.Fatal(err)
	}
	var qs []string
	for _, p := range pairs {
		q, err := Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		qs = append(qs, q)
	}
	got, err := Prove(z3, qs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "unsat unsat" {
		t.Errorf("got %v, want both runs proved", got)
	}
}

func TestEncodeRefusesAnUnmodelledOp(t *testing.T) {
	_, err := Encode(Pair{Before: ops(t, "call_direct f"), After: ops(t, "call_direct f")})
	var m *Mismatch
	if err == nil || errors.As(err, &m) {
		t.Fatalf("got %v, want a plain error", err)
	}
}

func TestSplitRefusesAChangedBarrier(t *testing.T) {
	for _, after := range []string{"call_direct g ; return", "return"} {
		rw := Rewrite{Before: ops(t, "call_direct f ; return"), After: ops(t, after)}
		if _, err := Split(rw); err == nil {
			t.Errorf("%q: a changed unmodelled op split cleanly", after)
		}
	}
}

// Package tv is translation validation for the self-hosted compiler's op-list
// passes (#5543): instead of sampling inputs, it proves with an SMT solver that
// a pass's output computes what its input did, for every input.
//
// The compiler writes a pass's input and output op lists to stderr when
// FERN_TV_DUMP names the pass (ir.fern's tv_dump). Parse reads them back, Split
// pairs up the straight-line runs that differ, Encode states each pair's
// equivalence as an SMT-LIB query, and Prove hands the queries to Z3: unsat is
// a proof, sat a counterexample. docs/TRANSLATION-VALIDATION.md has the model
// and what it leaves out.
package tv

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Op is one dumped op. Line is the dump's line, which spells every field, so
// two ops are the same op exactly when their lines are equal.
type Op struct {
	Name     string
	Kind     int
	I32      int32
	I64      int64
	Width    int
	Unsigned bool
	Str      string
	Line     string
}

// Rewrite is one run of a pass over one op list: what it read and what it
// wrote.
type Rewrite struct {
	Pass          string
	Before, After []Op
}

const prefix = "fern-tv "

// Parse reads every rewrite out of a compiler's stderr. Lines outside a
// rewrite's block are the compiler's other output and are skipped.
func Parse(stderr string) ([]Rewrite, error) {
	lines := strings.Split(stderr, "\n")
	var out []Rewrite
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], prefix) {
			continue
		}
		rw := Rewrite{Pass: strings.TrimPrefix(lines[i], prefix)}
		var err error
		if rw.Before, i, err = parseSide(lines, i+1, "before"); err != nil {
			return nil, err
		}
		if rw.After, i, err = parseSide(lines, i+1, "after"); err != nil {
			return nil, err
		}
		if i+1 >= len(lines) || lines[i+1] != prefix+"end" {
			return nil, fmt.Errorf("tv: %s rewrite does not end with %q", rw.Pass, prefix+"end")
		}
		i++
		out = append(out, rw)
	}
	return out, nil
}

// parseSide reads the `fern-tv <side> N` header at lines[at] and its N ops,
// returning them and the index of the last line read.
func parseSide(lines []string, at int, side string) ([]Op, int, error) {
	head := prefix + side + " "
	if at >= len(lines) || !strings.HasPrefix(lines[at], head) {
		return nil, at, fmt.Errorf("tv: line %d: want %q header", at+1, head)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(lines[at], head))
	if err != nil || at+n >= len(lines) {
		return nil, at, fmt.Errorf("tv: line %d: bad op count %q", at+1, lines[at])
	}
	ops := make([]Op, n)
	for k := range ops {
		if ops[k], err = parseOp(lines[at+1+k]); err != nil {
			return nil, at, fmt.Errorf("tv: line %d: %v", at+2+k, err)
		}
	}
	return ops, at + n, nil
}

// parseOp reads `name kind i32 i64 f64bits width unsigned decl :hexstr`.
func parseOp(line string) (Op, error) {
	f := strings.Fields(line)
	if len(f) != 9 || !strings.HasPrefix(f[8], ":") {
		return Op{}, fmt.Errorf("want 9 fields ending in :hex, got %q", line)
	}
	var nums [6]int64
	for k, s := range []string{f[1], f[2], f[3], f[4], f[5], f[7]} {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Op{}, fmt.Errorf("field %q of %q: %v", s, line, err)
		}
		nums[k] = v
	}
	str, err := hex.DecodeString(f[8][1:])
	if err != nil {
		return Op{}, fmt.Errorf("str of %q: %v", line, err)
	}
	return Op{
		Name: f[0], Kind: int(nums[0]), I32: int32(nums[1]), I64: nums[2],
		Width: int(nums[4]), Unsigned: f[6] == "1", Str: string(str), Line: line,
	}, nil
}

// String renders the op as a reader wants it in a report.
func (o Op) String() string {
	switch {
	case o.Kind == kConstI32 || o.Kind == kLoadLocal || o.Kind == kStoreLocal || o.Kind == kTeeLocal:
		return fmt.Sprintf("%s %d", o.Name, o.I32)
	case o.Kind == kConstI64 && o.Str == "":
		return fmt.Sprintf("%s %d", o.Name, o.I64)
	case o.Str != "":
		return fmt.Sprintf("%s %q", o.Name, o.Str)
	case o.Width != 0:
		return fmt.Sprintf("%s@%d", o.Name, o.Width)
	}
	return o.Name
}

// Render joins ops the way the compiler's drivers print an op list.
func Render(ops []Op) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = o.String()
	}
	return strings.Join(parts, " ; ")
}

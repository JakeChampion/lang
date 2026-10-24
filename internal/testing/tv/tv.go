package tv

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

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

type Rewrite struct {
	Pass          string
	Before, After []Op
}

const prefix = "fern-tv "

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

func Render(ops []Op) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = o.String()
	}
	return strings.Join(parts, " ; ")
}

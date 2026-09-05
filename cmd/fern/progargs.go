package main

import (
	"fmt"
	"strings"
)

// programArgs turns the command line remaining after FILE into the vector the
// program observes through `args()[1:]`. Both engines go through it, so an
// interpreted run and a compiled binary see the same arguments for the same
// command tail.
//
// One leading `--` separates driver flags from program arguments and is
// consumed here; a later `--` is ordinary data, so `-- -- x` passes a literal
// `--` through. Without that separator a leading `-…` is a driver flag written
// past the point Go's flag package stops parsing: accepting it would silently
// reinterpret it as program data, so it is refused instead. A bare `-` is the
// conventional stdin/stdout filename and stays data.
func programArgs(rest []string) ([]string, error) {
	if len(rest) == 0 {
		return nil, nil
	}
	if rest[0] == "--" {
		return rest[1:], nil
	}
	if a := rest[0]; len(a) > 1 && strings.HasPrefix(a, "-") {
		return nil, fmt.Errorf("fern: %[1]s comes after the source file, where driver flags are no longer parsed\n"+
			"  a driver flag goes before FILE; an argument for the program goes after a `--` separator:\n"+
			"    fern FILE.fern -- %[1]s", a)
	}
	return rest, nil
}

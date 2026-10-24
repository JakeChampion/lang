package main

import (
	"fmt"
	"strings"
)

func programArgs(rest []string) ([]string, error) {
	if len(rest) == 0 {
		return nil, nil
	}
	if rest[0] == "--" {
		return rest[1:], nil
	}
	if a := rest[0]; len(a) > 1 && strings.HasPrefix(a, "-") {
		return nil, fmt.Errorf("fern-oracle: %[1]s comes after the source file, where driver flags are no longer parsed\n"+
			"  a driver flag goes before FILE; an argument for the program goes after a `--` separator:\n"+
			"    fern-oracle FILE.fern -- %[1]s", a)
	}
	return rest, nil
}

func documentArgsError(args []string) string {
	extra := args[1]
	if len(extra) > 1 && strings.HasPrefix(extra, "-") {
		return fmt.Sprintf("fern-oracle: %s comes after the document, where flags are no longer parsed; put it before %s", extra, args[0])
	}
	return fmt.Sprintf("fern-oracle: one document expected, got %q and %q", args[0], extra)
}

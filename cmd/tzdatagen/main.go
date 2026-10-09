// Command tzdatagen regenerates the IANA zone table embedded in std/tz
// from the pinned tzdata release (internal/tables/tzdata).
//
// Usage, from the repository root:
//
//	go run ./cmd/tzdatagen
package main

import (
	"fmt"
	"os"

	"github.com/jakechampion/lang/internal/tables/tzdata"
)

func main() {
	src, err := os.ReadFile(tzdata.TzFern)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := tzdata.Generate(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(tzdata.TzFern, []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

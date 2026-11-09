// Command irkindgen writes the KIND_ORDER table of compiler/ir.fern from its
// KIND_NAMES list, so the name of each IR kind is written once.
//
//	go run ./cmd/irkindgen compiler/ir.fern
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	namesHead = "const KIND_NAMES: string[] = [\n"
	namesTail = "];\n"
	begin     = "// BEGIN GENERATED KIND ORDER (cmd/irkindgen) — do not edit by hand."
	end       = "// END GENERATED KIND ORDER"
)

var entry = regexp.MustCompile(`^  "([a-z0-9_]*)",  // ([0-9]+)$`)

// Names reads KIND_NAMES. Each entry is on its own line with its id as a
// comment, and the id must be the entry's position.
func Names(src string) ([]string, error) {
	at := strings.Index(src, namesHead)
	if at < 0 {
		return nil, fmt.Errorf("%q not found", strings.TrimSpace(namesHead))
	}
	body := src[at+len(namesHead):]
	stop := strings.Index(body, namesTail)
	if stop < 0 {
		return nil, fmt.Errorf("KIND_NAMES has no closing %q", strings.TrimSpace(namesTail))
	}
	var names []string
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(body[:stop], "\n"), "\n") {
		m := entry.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("KIND_NAMES line %q is not `  \"name\",  // id`", line)
		}
		id, _ := strconv.Atoi(m[2])
		if id != len(names) {
			return nil, fmt.Errorf("KIND_NAMES entry %q is at id %d but its comment says %d", m[1], len(names), id)
		}
		if m[1] != "" {
			if prev, dup := seen[m[1]]; dup {
				return nil, fmt.Errorf("KIND_NAMES names %q at ids %d and %d", m[1], prev, id)
			}
			seen[m[1]] = id
		}
		names = append(names, m[1])
	}
	return names, nil
}

// Rewrite fills the generated block with the ids of the named kinds, sorted
// by name in byte order, which is the order kind_id searches.
func Rewrite(src string) (string, error) {
	names, err := Names(src)
	if err != nil {
		return "", err
	}
	var ids []int
	for id, n := range names {
		if n != "" {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return names[ids[i]] < names[ids[j]] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	b := strings.Index(src, begin)
	if b < 0 {
		return "", fmt.Errorf("begin marker %q not found", begin)
	}
	e := strings.Index(src[b:], end)
	if e < 0 {
		return "", fmt.Errorf("end marker %q not found after its begin marker", end)
	}
	block := begin + "\nconst KIND_ORDER: i32[] = [" + strings.Join(parts, ", ") + "];\n\n"
	return src[:b] + block + src[b+e:], nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: irkindgen compiler/ir.fern")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out, err := Rewrite(string(src))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

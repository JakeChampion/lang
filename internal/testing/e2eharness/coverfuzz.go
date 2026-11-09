package e2eharness

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// The pieces of a coverage-guided fuzz loop over a binary that `fern -cover`
// built. Such a binary writes its counter table to stderr as it exits, so one
// run of it gives the coverage of one input. docs/COVERAGE.md has the format.

// CoverBits is the set of report rows that counted at least once. A row is
// known by its position, because the table of one binary is fixed.
type CoverBits struct {
	words []uint64
	Rows  int
}

func (c *CoverBits) Set(i int) {
	w := i / 64
	for len(c.words) <= w {
		c.words = append(c.words, 0)
	}
	c.words[w] |= 1 << (uint(i) % 64)
}

// AddAll adds the rows of other and returns how many were not in c.
func (c *CoverBits) AddAll(other *CoverBits) int {
	gained := 0
	for w, b := range other.words {
		for len(c.words) <= w {
			c.words = append(c.words, 0)
		}
		if fresh := b &^ c.words[w]; fresh != 0 {
			gained += bits.OnesCount64(fresh)
			c.words[w] |= fresh
		}
	}
	return gained
}

func (c *CoverBits) Count() int {
	n := 0
	for _, w := range c.words {
		n += bits.OnesCount64(w)
	}
	return n
}

// ReadCoverReport reads the stderr of a binary that -cover built. It returns
// the rows that counted, and the other lines, which are the program's own
// stderr.
func ReadCoverReport(r io.Reader) (*CoverBits, string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	got := &CoverBits{}
	var rest strings.Builder
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte(ast.CoverLinePrefix)) && !bytes.HasPrefix(line, []byte(ast.CoverBranchPrefix)) {
			rest.Write(line)
			rest.WriteByte('\n')
			continue
		}
		if sp := bytes.LastIndexByte(line, ' '); sp >= 0 && !bytes.Equal(line[sp+1:], []byte("0")) {
			got.Set(got.Rows)
		}
		got.Rows++
	}
	if err := sc.Err(); err != nil {
		return nil, rest.String(), fmt.Errorf("read coverage report: %w", err)
	}
	return got, rest.String(), nil
}

// LoadCoverCorpus reads each file of dir as one corpus entry, in name order.
// An empty or missing dir gives no entries.
func LoadCoverCorpus(dir string) [][]byte {
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out [][]byte
	for _, n := range names {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// SaveCoverCorpus writes each entry to dir under the SHA-256 of its bytes. An
// empty dir saves nothing.
func SaveCoverCorpus(t testing.TB, dir string, corpus [][]byte) {
	t.Helper()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("corpus dir %s: %v (discoveries not persisted)", dir, err)
		return
	}
	for _, entry := range corpus {
		sum := sha256.Sum256(entry)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%x", sum)), entry, 0o644); err != nil {
			t.Logf("write corpus entry: %v", err)
			return
		}
	}
}

// Unified-diff helper for the formatter's `-d` mode.
//
// Computes a line-level diff between the file as-written and its
// formatted form, emitting a familiar `--- a/file +++ b/file @@`
// unified-diff string. The implementation is a minimal LCS-based
// edit-script extractor — fine for the modest line counts a single
// source file produces, no external diff binary required so the CLI
// stays self-contained on Windows / WSL / wherever it runs.
//
// The diff intentionally lives in the printer package rather than
// alongside Format itself so a future tooling consumer (an LSP, a
// pre-commit hook) can pull it in without dragging the CLI in too.

package printer

import (
	"fmt"
	"strings"
)

// UnifiedDiff returns a unified-diff text of `before` against
// `after`, both expected to be \n-separated source listings.
// `pathBefore` and `pathAfter` populate the `--- ` / `+++ ` header
// rows. The result is empty when the two inputs are byte-identical.
//
// The hunk format is the conventional `@@ -a,b +c,d @@` with the
// surrounding context lines included. Tools that read unified diffs
// (`patch`, GitHub PR review, IDE diff viewers) accept the output
// without complaint.
func UnifiedDiff(before, after, pathBefore, pathAfter string) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)
	hunks := buildHunks(a, b, 3)
	if len(hunks) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", pathBefore)
	fmt.Fprintf(&sb, "+++ %s\n", pathAfter)
	for _, h := range hunks {
		sb.WriteString(formatHunk(h))
	}
	return sb.String()
}

// splitLines returns s broken on '\n' boundaries with the trailing
// newline retained on each line (so " " lines reproduce verbatim
// in the diff output). A trailing-empty entry from a final '\n'
// is dropped — the diff doesn't need a phantom empty line at
// the end.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, strings.Count(s, "\n")+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		// Trailing line with no \n — keep it so the diff faithfully
		// shows "no newline at end of file".
		out = append(out, s[start:])
	}
	return out
}

// op describes one entry in the LCS-derived edit script.
type op struct {
	kind byte // ' ' equal, '-' delete from a, '+' insert from b
	line string
}

// editScript runs LCS over a and b and returns the merge of the two
// reconstructed via back-pointers. Each op carries the line text
// (already with its trailing newline if any) so the hunk emitter
// just needs to glue prefixes and write.
func editScript(a, b []string) []op {
	m, n := len(a), len(b)
	// Classic LCS DP.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	out := []op{}
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			out = append(out, op{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, op{'-', a[i]})
			i++
		default:
			out = append(out, op{'+', b[j]})
			j++
		}
	}
	for ; i < m; i++ {
		out = append(out, op{'-', a[i]})
	}
	for ; j < n; j++ {
		out = append(out, op{'+', b[j]})
	}
	return out
}

// hunk is one contiguous region of the diff plus enough surrounding
// context (`contextLines` on each side) to make the output
// human-readable.
type hunk struct {
	aStart, aLen int
	bStart, bLen int
	ops          []op
}

// buildHunks partitions the full edit script into hunks separated by
// runs of `2*context+1` equal lines or longer (anything shorter than
// that gets merged into the surrounding hunk). The result is a list
// of hunks each containing only its own context plus changes; ops
// that fall in pure-equal runs *between* hunks are discarded.
func buildHunks(a, b []string, context int) []hunk {
	script := editScript(a, b)
	if len(script) == 0 {
		return nil
	}
	// Collect indices of changed ops, then expand by context.
	type changeRun struct{ lo, hi int }
	var runs []changeRun
	i := 0
	for i < len(script) {
		if script[i].kind == ' ' {
			i++
			continue
		}
		lo := i
		for i < len(script) && script[i].kind != ' ' {
			i++
		}
		runs = append(runs, changeRun{lo, i})
	}
	if len(runs) == 0 {
		return nil
	}
	// Merge runs whose context windows overlap.
	merged := []changeRun{runs[0]}
	for _, r := range runs[1:] {
		last := &merged[len(merged)-1]
		gap := r.lo - last.hi
		if gap <= 2*context {
			last.hi = r.hi
		} else {
			merged = append(merged, r)
		}
	}
	// Emit a hunk per merged run, with `context` lines of surrounding
	// equal context tacked on each side (clipped to the script
	// boundaries).
	out := []hunk{}
	for _, r := range merged {
		start := r.lo - context
		if start < 0 {
			start = 0
		}
		end := r.hi + context
		if end > len(script) {
			end = len(script)
		}
		// Compute aStart / bStart by counting the prefix of the
		// script before `start`.
		aPos, bPos := 0, 0
		for k := 0; k < start; k++ {
			switch script[k].kind {
			case ' ':
				aPos++
				bPos++
			case '-':
				aPos++
			case '+':
				bPos++
			}
		}
		aLen, bLen := 0, 0
		for k := start; k < end; k++ {
			switch script[k].kind {
			case ' ':
				aLen++
				bLen++
			case '-':
				aLen++
			case '+':
				bLen++
			}
		}
		out = append(out, hunk{
			aStart: aPos + 1, // unified-diff line numbers are 1-based
			aLen:   aLen,
			bStart: bPos + 1,
			bLen:   bLen,
			ops:    script[start:end],
		})
	}
	return out
}

// formatHunk turns one hunk into its `@@ -a,b +c,d @@\n<lines>`
// string. Each context / change line includes its prefix and the
// original trailing newline.
func formatHunk(h hunk) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.aStart, h.aLen, h.bStart, h.bLen)
	for _, op := range h.ops {
		sb.WriteByte(op.kind)
		sb.WriteString(op.line)
		// Each line in the script already carries its trailing
		// newline. If a final line lacks one (no-newline-at-eof
		// case), append a `\n` so the diff itself stays
		// well-formed.
		if len(op.line) == 0 || op.line[len(op.line)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

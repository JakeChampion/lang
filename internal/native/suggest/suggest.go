// Package suggest offers the closest known spelling for an unrecognised
// mnemonic, so an assembler's "unsupported instruction" error can name the
// thing the author probably meant.
//
// Shared by the x86-64 and arm64 assemblers. The algorithm is target-agnostic
// and only the vocabulary differs, so keeping one copy avoids the drift that
// two hand-maintained edit-distance implementations invite — the same reason
// internal/native/cfi is shared.
package suggest

// Closest returns the nearest candidate when `word` is a near-miss — one edit
// for short names, two for longer, which is enough to catch a typo or a
// wrong-ISA spelling — or "" when nothing is close enough to be worth saying.
//
// Candidates are expected sorted; ties go to the first, so the message is
// deterministic rather than dependent on map iteration order.
func Closest(word string, candidates []string) string {
	if len(word) < 2 {
		return ""
	}
	maxDist := 1
	if len(word) > 4 {
		maxDist = 2
	}
	best, bestDist := "", maxDist+1
	for _, c := range candidates {
		if d := Distance(word, c, maxDist); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// Distance is the optimal-string-alignment distance (Levenshtein plus
// adjacent transposition), capped: once every entry in a row exceeds max the
// true distance cannot come back under it, so max+1 is returned early.
func Distance(a, b string, max int) int {
	if len(a)-len(b) > max || len(b)-len(a) > max {
		return max + 1
	}
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < d {
					d = t
				}
			}
			cur[j] = d
			if d < rowMin {
				rowMin = d
			}
		}
		if rowMin > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	if prev[len(b)] > max {
		return max + 1
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

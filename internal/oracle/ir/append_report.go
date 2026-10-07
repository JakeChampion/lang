package ir

import (
	"fmt"
	"sort"
	"strings"
)

// FormatAppendSites renders every `.append` site in p and the decision
// emitArrayPush made there, for `fern -append-report` (#6992). This is the
// retained Go lowering's decision, not the primary compiler's (#11251).
//
// A copying append reallocates and copies the whole buffer, so one inside
// a loop is O(n²) bytes.
//
// The decision reads only types and AST shape (see appendDecision), so the
// report does not depend on the target's pointer width.
func FormatAppendSites(p *Program) string {
	var sites []AppendSite
	for _, f := range p.Funcs {
		sites = append(sites, f.AppendSites...)
	}
	if len(sites) == 0 {
		return "no .append sites\n"
	}
	sort.SliceStable(sites, func(i, j int) bool {
		a, b := sites[i], sites[j]
		if a.Func != b.Func {
			return a.Func < b.Func
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})

	posW, recvW, copying := 0, 0, 0
	pos := make([]string, len(sites))
	for i, s := range sites {
		pos[i] = fmt.Sprintf("%s:%d:%d", s.Func, s.Line, s.Col)
		posW = max(posW, len(pos[i]))
		recvW = max(recvW, len(s.Recv))
		if s.Copies {
			copying++
		}
	}

	var b strings.Builder
	for i, s := range sites {
		verdict := "in place"
		if s.Copies {
			verdict = "COPY    "
		}
		fmt.Fprintf(&b, "%-*s  %s  %-*s  %s\n", posW, pos[i], verdict, recvW, s.Recv, s.Reason)
	}
	fmt.Fprintf(&b, "\n%d append site(s), %d copying\n", len(sites), copying)
	return b.String()
}

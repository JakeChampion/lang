package x86_64ssa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A runtime helper is called like any other function, so it owes its caller
// rbx and r12–r15 untouched. Nothing checked that before: the allocatable file
// held only rbx, so a helper clobbering r12–r15 hit registers the emitter used
// as scratch, which is dead across a call and never noticed. Now that the
// allocator homes call-crossing values in all five, a helper that clobbers one
// corrupts a live value in its caller — a wrong answer with nothing failing at
// the point of the bug.
//
// The check is per helper body: every callee-saved register it names must be
// pushed before any other use and popped as many times as it is pushed. The
// arm64 twin is TestRuntimeHelpersPreserveCalleeSaved.
func TestRuntimeHelperBodiesPreserveCalleeSaved(t *testing.T) {
	names := make([]string, 0, len(runtimeHelperEmitters))
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			runtimeHelperEmitters[name](func(format string, args ...any) {
				fmt.Fprintf(&b, format+"\n", args...)
			})
			body := b.String()
			for r := range calleeSavedNames {
				checkPreserved(t, name, r, body)
			}
		})
	}
}

// checkPreserved reports whether helper body `body` leaves 64-bit register r as
// it found it: either it never mentions r at any width, or its first mention is
// `push r` and its pushes and pops of r balance.
func checkPreserved(t *testing.T, helper, r, body string) {
	t.Helper()
	spellings := regSpellings(gpIndex(r))
	mention := regexp.MustCompile(`\b(` + strings.Join(spellings[:], "|") + `)\b`)

	pushes, pops, first := 0, 0, ""
	for _, line := range strings.Split(body, "\n") {
		ln := strings.TrimSpace(line)
		if i := strings.Index(ln, "//"); i >= 0 {
			ln = strings.TrimSpace(ln[:i])
		}
		if !mention.MatchString(ln) {
			continue
		}
		if first == "" {
			first = ln
		}
		switch ln {
		case "push " + r:
			pushes++
		case "pop " + r:
			pops++
		}
	}
	if first == "" {
		return // never touched
	}
	if first != "push "+r {
		t.Errorf("%s: first use of %s is %q, not a push — the caller's value is gone", helper, r, first)
	}
	if pushes != pops {
		t.Errorf("%s: %s is pushed %d time(s) and popped %d — the caller gets a shifted stack or a clobbered register",
			helper, r, pushes, pops)
	}
}

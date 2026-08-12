package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #6642 slice 1: doc-comment association, the join between the lexer's comment
// stream (#6739) and the parser's declarations (#6718, #6731).
//
// The rendering half of ferndoc is not here yet. The association is, and it is
// the half with semantics — a run of comments documents the declaration
// directly below it, a blank line breaks the binding, and a stateful cursor
// stops any comment reaching two declarations.
//
// Two gates, and the second is the one that matters:
//
//   - `-self` runs the pass's own case set (both directions, hand-built
//     positions), so a break names the case that broke.
//   - the differential below runs the pass over the REAL stdlib and compares
//     what it bound against what `cmd/ferndoc` bound to the same declaration.
//     Hand-built cases pin the semantics I intended; only the differential
//     pins the semantics native actually has.

// TestSelfHostFerndocSelfChecks runs the driver's own assertions. A non-zero
// exit is the failing case's id.
func TestSelfHostFerndocSelfChecks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ferndoc_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ferndoc_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ferndoc_run.fern", "ferndoc_run")

	cmd := exec.Command(bin, "-self")
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("ferndoc_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("ferndoc_run -self exit code = %d, want 0 (the code is the failing case's id)\n%s", code, out)
	}
	if want := "ferndoc: association agrees"; !strings.Contains(string(out), want) {
		t.Errorf("ferndoc_run -self stdout = %q, want it to contain %q", out, want)
	}
}

// ferndocSkipShapes is why a stdlib module is left out of the differential:
// the self-host front end does not carry the declaration shape, so its
// declarations never call take_doc and the cursor lands somewhere native's
// does not.
//
// Both entries are the remaining decl-shape work on #6642, not association
// bugs. `const` desugars to a zero-arg function here (recoverable through
// FuncDecl.is_const); `trait` exists only as TraitReq and carries no
// visibility at all, so `pub trait` has no representation.
//
// The list is DERIVED from the sources rather than written down, so a module
// that grows a trait tomorrow leaves the differential automatically instead of
// failing it — and the count assertion below is what stops that from quietly
// hollowing the gate out.
func ferndocSkipShapes(src string) string {
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "pub trait ") || strings.HasPrefix(t, "trait ") {
			return "trait"
		}
		if strings.HasPrefix(t, "pub const ") || strings.HasPrefix(t, "const ") {
			return "const"
		}
	}
	return ""
}

// nativeDocs parses one of cmd/ferndoc's generated pages into heading → doc.
//
// The page shape per declaration is fixed: a `## ` heading, a blank line, a
// fenced `fern` signature, then the doc paragraph if there is one. So the doc
// is everything after the closing fence up to the next heading.
func nativeDocs(page string) map[string]string {
	out := map[string]string{}
	blocks := strings.Split(page, "\n## ")
	for i, b := range blocks {
		if i == 0 {
			continue // front matter + title, before the first heading
		}
		nl := strings.Index(b, "\n")
		if nl < 0 {
			continue
		}
		heading := strings.TrimSpace(b[:nl])
		rest := b[nl+1:]
		// Past the signature fence. The opening fence is "```fern"; the
		// closing one is the next "```" line.
		open := strings.Index(rest, "```fern\n")
		if open < 0 {
			continue
		}
		after := rest[open+len("```fern\n"):]
		close := strings.Index(after, "\n```\n")
		if close < 0 {
			continue
		}
		out[heading] = strings.TrimSpace(after[close+len("\n```\n"):])
	}
	return out
}

// unescapeDoc reverses ferndoc_run's escaping. Backslash first, so a literal
// `\n` in the source prose — the stdlib's CSV and HTTP modules have several —
// comes back as two characters rather than as a newline.
func unescapeDoc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// selfHeading renders a DocDecl the way cmd/ferndoc titles it, so the two
// sides key on the same string.
func selfHeading(kind, name string) string {
	switch kind {
	case "struct":
		return "`struct " + name + "`"
	case "enum":
		return "`enum " + name + "`"
	default:
		return "`" + name + "`"
	}
}

// TestSelfHostFerndocMatchesNative is the differential: for every stdlib
// module whose declaration shapes the self-host front end carries, every
// declaration must be bound to the same doc text by both compilers.
//
// This is what makes the port a port rather than a reimplementation. Native's
// association has behaviour nobody would design on purpose — a trailing
// comment on the line above a declaration is absorbed as that declaration's
// doc, because the walk looks only at lines — and the hand-built cases can
// only pin what I already believed. The stdlib is 100+ real files; it is where
// a divergence I did not think of shows up.
func TestSelfHostFerndocMatchesNative(t *testing.T) {
	if testing.Short() {
		t.Skip("stdlib sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ferndoc_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ferndoc_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ferndoc_run.fern", "ferndoc_run")

	// Generate the native pages once, from the same sources.
	docDir := t.TempDir()
	gen := exec.Command("go", "run", "./../../cmd/ferndoc", "-out", docDir)
	gen.Dir = "."
	var genErr bytes.Buffer
	gen.Stderr = &genErr
	if err := gen.Run(); err != nil {
		t.Fatalf("running cmd/ferndoc: %v\n%s", err, genErr.String())
	}

	stdDir := filepath.Join(langSrcAbs(t, "internal"), "stdlib", "std")
	srcs, err := filepath.Glob(filepath.Join(stdDir, "*.fern"))
	if err != nil {
		t.Fatalf("globbing std: %v", err)
	}
	if len(srcs) < 40 {
		t.Fatalf("found %d std modules, expected the full set — a shrunken sweep proves nothing", len(srcs))
	}

	var compared, skipped, withDoc int
	for _, s := range srcs {
		base := strings.TrimSuffix(filepath.Base(s), ".fern")
		if strings.HasPrefix(base, "_test_") {
			continue
		}
		src, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("reading %s: %v", s, err)
		}
		if why := ferndocSkipShapes(string(src)); why != "" {
			skipped++
			continue
		}
		page, err := os.ReadFile(filepath.Join(docDir, base+".md"))
		if err != nil {
			// ferndoc skips modules with no public decls; so does this.
			continue
		}
		want := nativeDocs(string(page))
		if len(want) == 0 {
			continue
		}

		cmd := exec.Command(bin)
		cmd.Stdin = bytes.NewReader(src)
		out, rerr := cmd.Output()
		if rerr != nil {
			t.Errorf("%s: self-host ferndoc failed: %v", base, rerr)
			continue
		}
		got := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if line == "" || strings.HasPrefix(line, "intro:") {
				continue
			}
			// "<kind> <name> <line>:<col> <doc>"
			f := strings.SplitN(line, " ", 4)
			if len(f) < 3 {
				continue
			}
			doc := ""
			if len(f) == 4 {
				doc = unescapeDoc(f[3])
			}
			got[selfHeading(f[0], f[1])] = doc
		}

		for heading, wantDoc := range want {
			gotDoc, ok := got[heading]
			if !ok {
				t.Errorf("%s: native documents %s, the self-host pass did not collect it", base, heading)
				continue
			}
			if gotDoc != wantDoc {
				t.Errorf("%s: doc for %s differs\n--- self-host ---\n%s\n--- native ---\n%s",
					base, heading, gotDoc, wantDoc)
				continue
			}
			if wantDoc != "" {
				withDoc++
			}
		}
		compared++
	}

	if compared < 20 {
		t.Errorf("compared only %d modules (skipped %d for unported decl shapes) — the differential has gone hollow", compared, skipped)
	}
	// Agreeing that everything is undocumented would agree perfectly and prove
	// nothing, so the count of declarations that actually carried a doc is
	// asserted too.
	if withDoc < 200 {
		t.Errorf("only %d declarations carried a doc on both sides — the differential is agreeing about emptiness", withDoc)
	}
	t.Logf("ferndoc differential: %d modules compared (%d documented decls matched), %d skipped for unported decl shapes", compared, withDoc, skipped)
}

package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #6642: the self-hosted doc generator — doc-comment association (the join
// between the lexer's comment stream, #6739, and the parser's declarations,
// #6718 / #6731) and the Markdown rendering over it.
//
// Three gates, in ascending order of what they prove:
//
//   - `-self` runs the association's own case set (both directions, hand-built
//     positions), so a break names the case that broke.
//   - the association differential runs the pass over the REAL stdlib and
//     compares what it bound against what `cmd/ferndoc` bound to the same
//     declaration. Hand-built cases pin the semantics I intended; only this
//     pins the semantics native actually has.
//   - the page differential compares the whole rendered page BYTE FOR BYTE,
//     which is the contract a doc generator actually has.

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
	case "trait":
		return "`trait " + name + "`"
	case "const":
		return "`const " + name + "`"
	default:
		return "`" + name + "`"
	}
}

// TestSelfHostFerndocMatchesNative is the differential: for every stdlib
// module, every declaration must be bound to the same doc text by both
// compilers. No module is exempt — the last exemption was `const`, which the
// self-host front end desugars to a zero-arg function and ferndoc now recovers
// through FuncDecl.is_const.
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

	// Both import roots, for the reason the page differential sweeps both:
	// `core` carries the container and comparison modules, and a gate that
	// skipped them would leave the densest generic code unchecked.
	var srcs []string
	for _, root := range []string{"std", "core"} {
		found, err := filepath.Glob(filepath.Join(langSrcAbs(t, "internal"), "stdlib", root, "*.fern"))
		if err != nil {
			t.Fatalf("globbing %s: %v", root, err)
		}
		srcs = append(srcs, found...)
	}
	if len(srcs) < 45 {
		t.Fatalf("found %d stdlib modules, expected the full set — a shrunken sweep proves nothing", len(srcs))
	}

	var compared, withDoc int
	for _, s := range srcs {
		base := strings.TrimSuffix(filepath.Base(s), ".fern")
		if strings.HasPrefix(base, "_test_") {
			continue
		}
		src, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("reading %s: %v", s, err)
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
		t.Errorf("compared only %d modules — the differential has gone hollow", compared)
	}
	// Agreeing that everything is undocumented would agree perfectly and prove
	// nothing, so the count of declarations that actually carried a doc is
	// asserted too.
	if withDoc < 200 {
		t.Errorf("only %d declarations carried a doc on both sides — the differential is agreeing about emptiness", withDoc)
	}
	t.Logf("ferndoc differential: %d modules compared (%d documented decls matched)", compared, withDoc)
}

// TestSelfHostFerndocPagesMatchNative renders every stdlib module — `std` and
// `core` both — through the self-hosted generator and compares the page with
// cmd/ferndoc's byte for byte.
//
// The comparison is UNCONDITIONAL. It used to carry a list of modules allowed
// to diverge, one per declaration shape the self-host front end did not keep;
// the last of them was `const`, and with that ported there is no module the
// generator is permitted to get wrong.
//
// Byte-for-byte is the gate #6642 asked for, and it is worth the strictness: a
// doc generator's whole output is its contract, so front matter, fence
// language, the blank line after a signature and the trailing comma inside an
// enum body all have to agree. Nothing weaker would notice most of what can go
// wrong here.
func TestSelfHostFerndocPagesMatchNative(t *testing.T) {
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

	docDir := t.TempDir()
	gen := exec.Command("go", "run", "./../../cmd/ferndoc", "-out", docDir)
	gen.Dir = "."
	var genErr bytes.Buffer
	gen.Stderr = &genErr
	if err := gen.Run(); err != nil {
		t.Fatalf("running cmd/ferndoc: %v\n%s", err, genErr.String())
	}

	// BOTH namespaces: `std` and `core` are separate import roots and
	// cmd/ferndoc gives each a page, so a gate over `std` alone would leave the
	// container and comparison modules — some of the densest generic code in
	// the tree — unchecked.
	var srcs []string
	roots := map[string]string{}
	for _, root := range []string{"std", "core"} {
		dir := filepath.Join(langSrcAbs(t, "internal"), "stdlib", root)
		found, err := filepath.Glob(filepath.Join(dir, "*.fern"))
		if err != nil {
			t.Fatalf("globbing %s: %v", root, err)
		}
		for _, f := range found {
			srcs = append(srcs, f)
			roots[f] = root
		}
	}
	if len(srcs) < 45 {
		t.Fatalf("found %d stdlib modules, expected the full set — a shrunken sweep proves nothing", len(srcs))
	}

	var matched int
	for _, s := range srcs {
		base := strings.TrimSuffix(filepath.Base(s), ".fern")
		if strings.HasPrefix(base, "_test_") {
			continue
		}
		src, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("reading %s: %v", s, err)
		}
		want, err := os.ReadFile(filepath.Join(docDir, base+".md"))
		if err != nil {
			continue // ferndoc emits no page for a module with no public decls
		}

		cmd := exec.Command(bin, "-page", roots[s]+"/"+base)
		cmd.Stdin = bytes.NewReader(src)
		got, rerr := cmd.Output()
		if rerr != nil {
			t.Errorf("%s: self-host ferndoc failed: %v", base, rerr)
			continue
		}

		if !bytes.Equal(got, want) {
			t.Errorf("%s: rendered page differs from cmd/ferndoc\n%s", base, firstPageDiff(string(want), string(got)))
			continue
		}
		matched++
	}

	if matched < 50 {
		t.Errorf("only %d modules rendered byte-identically (of %d) — the gate has gone hollow", matched, len(srcs))
	}
	t.Logf("ferndoc page differential: %d modules byte-identical", matched)
}

// TestSelfHostFernDocModeMatchesFerndoc drives `-doc` on the real `fern` CLI
// and compares the whole OUTPUT DIRECTORY with `ferndoc -out DIR`.
//
// The page differential above renders one module at a time through a driver
// that exists only to be tested. This one is the gate on everything the CLI
// mode adds around that: which files it decides are modules, how it derives a
// module path from a directory layout, which subdirectories it descends, how it
// flattens a nested module's page name, and that a module with nothing public
// produces no file rather than an empty one. None of that is reachable from
// `ferndoc_run`, and all of it is what a user of `fern -doc` gets.
//
// It also pins an equivalence the two tools do not otherwise share: cmd/ferndoc
// reads the stdlib EMBEDDED at build time, `fern -doc` reads it off disk, and
// the pages agreeing says those are the same sources.
func TestSelfHostFernDocModeMatchesFerndoc(t *testing.T) {
	if testing.Short() {
		t.Skip("stdlib sweep is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the -doc mode takes host filesystem paths as argv; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	nativeDir := t.TempDir()
	gen := exec.Command("go", "run", "./../../cmd/ferndoc", "-out", nativeDir)
	gen.Dir = "."
	var genErr bytes.Buffer
	gen.Stderr = &genErr
	if err := gen.Run(); err != nil {
		t.Fatalf("running cmd/ferndoc: %v\n%s", err, genErr.String())
	}

	// One invocation per namespace, into ONE directory — the namespace is the
	// directory's own name, so this is how `-doc` reproduces a tool that has
	// both roots baked in.
	selfDir := t.TempDir()
	stdlib := filepath.Join(langSrcAbs(t, "internal"), "stdlib")
	for _, root := range []string{"std", "core"} {
		cmd := exec.Command(fernBin, "-doc", filepath.Join(stdlib, root), "-o", selfDir)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("fern -doc %s: %v\n%s", root, err, stderr.String())
		}
	}

	wantPages := pageSet(t, nativeDir)
	gotPages := pageSet(t, selfDir)
	if len(wantPages) < 45 {
		t.Fatalf("cmd/ferndoc produced %d pages, expected the full stdlib — a shrunken sweep proves nothing", len(wantPages))
	}
	for name, want := range wantPages {
		got, ok := gotPages[name]
		if !ok {
			t.Errorf("ferndoc wrote %s; fern -doc did not", name)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: fern -doc differs from ferndoc\n%s", name, firstPageDiff(string(want), string(got)))
		}
	}
	// The other direction: a page nobody asked for is as wrong as a missing
	// one, and it is the direction a too-loose module filter fails in.
	for name := range gotPages {
		if _, ok := wantPages[name]; !ok {
			t.Errorf("fern -doc wrote %s; ferndoc did not", name)
		}
	}
	t.Logf("fern -doc: %d pages, identical to cmd/ferndoc", len(wantPages))

	// A single module argument writes ONE page to stdout, titled from its
	// containing directory the same way the tree walk titles it.
	one := exec.Command(fernBin, "-doc", filepath.Join(stdlib, "std", "io.fern"))
	got, err := one.Output()
	if err != nil {
		t.Fatalf("fern -doc <file>: %v", err)
	}
	if want := wantPages["io.md"]; !bytes.Equal(got, want) {
		t.Errorf("fern -doc std/io.fern differs from ferndoc's io.md\n%s", firstPageDiff(string(want), string(got)))
	}

	// A directory with nowhere to put its pages is a usage error, not a
	// silent dump of 69 concatenated pages onto stdout.
	noOut := exec.Command(fernBin, "-doc", filepath.Join(stdlib, "std"))
	out, _ := noOut.Output()
	if code := noOut.ProcessState.ExitCode(); code != 2 {
		t.Errorf("fern -doc <DIR> with no -o: exit = %d, want 2", code)
	}
	if len(out) != 0 {
		t.Errorf("fern -doc <DIR> with no -o wrote %d bytes to stdout, want none", len(out))
	}
}

// pageSet reads a directory of generated pages into name → bytes.
func pageSet(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading %s: %v", e.Name(), rerr)
		}
		out[e.Name()] = b
	}
	return out
}

// firstPageDiff reports the first differing line with a little context. A whole
// page is thousands of bytes and a full dump buries the one line that moved.
func firstPageDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("  line %d\n  --- native   --- %q\n  --- self-host --- %q", i+1, wl, gl)
		}
	}
	return "  (identical line-wise; trailing bytes differ)"
}

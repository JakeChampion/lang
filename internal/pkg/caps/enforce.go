package caps

import (
	"fmt"
	"strings"
)

// Grant is one package's enforcement input, assembled by the caller
// (cmd/fern) from the loaded manifests:
//
//   - Root: the root package (and everything folded into it) is never
//     enforced or warned — the program's author is trusting themselves
//     (docs/PACKAGE-CAPABILITIES-BRIEF.md: grants restrict dependencies).
//   - Governed: some dependency entry declaring this package carries a
//     `capabilities` key; reaching outside Caps is then an error.
//   - Caps: the granted capabilities (the union when several manifests
//     grant the same package). Meaningful only when Governed.
//
// A package with no Grant entry gets the zero value — not root, not
// governed — i.e. warn-and-allow, the migration default for
// dependencies whose manifests predate the capabilities key.
type Grant struct {
	Root     bool
	Governed bool
	Caps     []string
}

// Violation is one package reaching a capability outside its grant.
// Governed selects the severity: true is a checker-grade error (E070),
// false a warn-and-allow diagnostic. Chain is Analyze's example call
// chain, ending in the tagged builtin.
type Violation struct {
	Package    string
	Capability string
	Chain      []string
	Governed   bool
}

// Message renders the violation's human-readable text (without the
// `error[E070]:` / `warning:` label — the caller owns severity
// rendering).
func (v Violation) Message() string {
	builtin := v.Chain[len(v.Chain)-1]
	chain := strings.Join(v.Chain, " → ")
	if v.Governed {
		return fmt.Sprintf("package %q reaches '%s' (%s) without a capability grant: %s; add %q to its capabilities in fern.toml or remove the call",
			v.Package, v.Capability, builtin, chain, v.Capability)
	}
	return fmt.Sprintf("package %q reaches '%s' (%s) but no capabilities key governs it: %s; add capabilities = [...] to its dependency entry in fern.toml (ungoverned packages will become errors once default-deny lands)",
		v.Package, v.Capability, builtin, chain)
}

// Enforce filters Analyze's report rows against the per-package grants
// — the same traversal serves both modes: the report is all usage,
// enforcement is usage minus grants. Returns the governed violations
// (errors) and the ungoverned ones (warnings), each at most one per
// package+capability with Analyze's example chain, in Analyze's
// deterministic order (package, then capability).
func Enforce(rows []Row, grants map[string]Grant) (errs, warns []Violation) {
	for _, r := range rows {
		g := grants[r.Package]
		if g.Root {
			continue
		}
		allowed := map[string]bool{}
		for _, c := range g.Caps {
			allowed[c] = true
		}
		for _, u := range r.Uses {
			switch {
			case g.Governed && !allowed[u.Capability]:
				errs = append(errs, Violation{Package: r.Package, Capability: u.Capability, Chain: u.Chain, Governed: true})
			case !g.Governed:
				warns = append(warns, Violation{Package: r.Package, Capability: u.Capability, Chain: u.Chain})
			}
		}
	}
	return errs, warns
}

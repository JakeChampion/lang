package caps

import (
	"fmt"
	"strings"
)

type Grant struct {
	Root     bool
	Governed bool
	Caps     []string
}

type Violation struct {
	Package    string
	Name       string
	Capability string
	Chain      []string
	Governed   bool
}

func (v Violation) Message() string {
	name := v.Name
	if name == "" {
		name = v.Package
	}
	builtin := v.Chain[len(v.Chain)-1]
	chain := strings.Join(v.Chain, " → ")
	if v.Governed {
		return fmt.Sprintf("package %q reaches '%s' (%s) without a capability grant: %s; add %q to its capabilities in fern.toml or remove the call",
			name, v.Capability, builtin, chain, v.Capability)
	}
	return fmt.Sprintf("package %q reaches '%s' (%s) but no capabilities key governs it: %s; add capabilities = [...] to its dependency entry in fern.toml (ungoverned packages will become errors once default-deny lands)",
		name, v.Capability, builtin, chain)
}

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
				errs = append(errs, Violation{Package: r.Package, Name: r.Name, Capability: u.Capability, Chain: u.Chain, Governed: true})
			case !g.Governed:
				warns = append(warns, Violation{Package: r.Package, Name: r.Name, Capability: u.Capability, Chain: u.Chain})
			}
		}
	}
	return errs, warns
}

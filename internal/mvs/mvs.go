package mvs

import (
	"fmt"
	"sort"
)

// Selected is one resolved package: the exact version MVS chose and its
// source. The full set is what gets written to fern.lock.
type Selected struct {
	Name    string
	Version Version
	Source  Source
}

// DepsOf returns the version-dependencies (name → min version string) of
// a specific package version, given its source. The caller materializes
// the source (a local dir, or a store dir after fetching a url) and
// reads its manifest's version deps. Returning the source lets internal/
// mvs stay free of manifest/pkgcache.
type DepsOf func(pkg string, v Version, src Source) (map[string]string, error)

// Resolve runs Minimum Version Selection: starting from rootDeps
// (name → declared minimum), keep per package the MAXIMUM of the minimums
// demanded anywhere in the transitive graph, expanding each selected
// version's own requirements to a fixpoint. The result is deterministic
// and lockfile-ready. A demanded version absent from the index is a
// hard, precisely-located error (never a silent round-up).
func Resolve(rootDeps map[string]string, ix *Index, depsOf DepsOf) ([]Selected, error) {
	selected := map[string]Version{}
	var queue []string // packages whose selected version's deps need expanding

	raise := func(pkg, minS string) error {
		v, err := ParseVersion(minS)
		if err != nil {
			return fmt.Errorf("dependency %q: %w", pkg, err)
		}
		if _, ok := ix.SourceFor(pkg, v); !ok {
			avail := ix.Versions(pkg)
			return fmt.Errorf("dependency %q has no version %s in the index (available: %s)", pkg, v, versList(avail))
		}
		if cur, ok := selected[pkg]; !ok || v.Compare(cur) > 0 {
			selected[pkg] = v
			queue = append(queue, pkg)
		}
		return nil
	}

	for _, pkg := range sortedKeys(rootDeps) {
		if err := raise(pkg, rootDeps[pkg]); err != nil {
			return nil, err
		}
	}

	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		v := selected[pkg]
		src, _ := ix.SourceFor(pkg, v)
		deps, err := depsOf(pkg, v, src)
		if err != nil {
			return nil, fmt.Errorf("reading deps of %s@%s: %w", pkg, v, err)
		}
		for _, d := range sortedKeys(deps) {
			// A version raised above v since we enqueued it supersedes this
			// pass; the higher version is (or will be) its own queue entry.
			if cur, ok := selected[pkg]; ok && cur.Compare(v) != 0 {
				break
			}
			if err := raise(d, deps[d]); err != nil {
				return nil, fmt.Errorf("via %s@%s: %w", pkg, v, err)
			}
		}
	}

	out := make([]Selected, 0, len(selected))
	for _, pkg := range sortedVersionKeys(selected) {
		v := selected[pkg]
		src, _ := ix.SourceFor(pkg, v)
		out = append(out, Selected{Name: pkg, Version: v, Source: src})
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedVersionKeys(m map[string]Version) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func versList(vs []Version) string {
	if len(vs) == 0 {
		return "none"
	}
	s := ""
	for i, v := range vs {
		if i > 0 {
			s += ", "
		}
		s += v.String()
	}
	return s
}

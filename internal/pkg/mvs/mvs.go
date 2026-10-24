package mvs

import (
	"fmt"
	"sort"
)

type Selected struct {
	Name    string
	Version Version
	Source  Source
}

type DepsOf func(pkg string, v Version, src Source) (map[string]string, error)

func Resolve(rootDeps map[string]string, excludes map[string][]string, ix *Index, depsOf DepsOf) ([]Selected, error) {
	selected := map[string]Version{}
	var queue []string

	excluded := func(pkg string, v Version) bool {
		for _, s := range excludes[pkg] {
			if e, err := ParseVersion(s); err == nil && e.Compare(v) == 0 {
				return true
			}
		}
		return false
	}

	raise := func(pkg, minS string) error {
		v, err := ParseVersion(minS)
		if err != nil {
			return fmt.Errorf("dependency %q: %w", pkg, err)
		}
		if excluded(pkg, v) {
			up, ok := Version{}, false
			for _, cand := range ix.Versions(pkg) {
				if cand.Compare(v) > 0 && !excluded(pkg, cand) {
					up, ok = cand, true
					break
				}
			}
			if !ok {
				return fmt.Errorf("dependency %q: version %s is excluded by the top-level manifest and the index has no higher non-excluded version (available: %s)", pkg, v, versList(ix.Versions(pkg)))
			}
			v = up
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

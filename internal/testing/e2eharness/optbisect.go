package e2eharness

import (
	"sort"
	"strconv"
	"strings"
)

type OptDecision struct {
	Hash       uint64
	Pass, Name string
}

func (d OptDecision) String() string { return d.Pass + " " + d.Name }

func ParseOptTrace(out string) []OptDecision {
	seen := map[uint64]bool{}
	var ds []OptDecision
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != "opt-decision" {
			continue
		}
		h, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil || seen[h] {
			continue
		}
		seen[h] = true
		ds = append(ds, OptDecision{Hash: h, Pass: f[2], Name: f[3]})
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].Hash < ds[j].Hash })
	return ds
}

func OptBisectEnv(env []string, bound uint64) []string {
	return append(append([]string{}, env...), "FERN_OPT_BISECT="+strconv.FormatUint(bound, 10))
}

func BisectOptDecisions(ds []OptDecision, agrees func(bound uint64) bool) (culprit OptDecision, ok bool) {
	if len(ds) == 0 || !agrees(ds[len(ds)-1].Hash+1) {
		return OptDecision{}, false
	}
	lo, hi := 0, len(ds)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if agrees(ds[mid].Hash + 1) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return ds[lo], true
}

package corpus

import (
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Shard selects one index/count partition of a list of repository paths.
// An empty selector retains the complete list.
func Shard(files []string, selector string) ([]string, error) {
	return ShardBy(files, selector, func(file string) string { return file })
}

// ShardBy selects one index/count partition of items. The hash of an item
// name selects its partition. Thus an item stays in its partition when other
// items join or leave the list. A CI shard restores its own cache slot first,
// and a verdict in that slot is useful only to the items that stay there.
// An empty selector retains the complete list.
func ShardBy[T any](items []T, selector string, name func(T) string) ([]T, error) {
	if selector == "" {
		return items, nil
	}
	index, count, err := parseSelector(selector)
	if err != nil {
		return nil, err
	}
	var selected []T
	for _, item := range items {
		h := fnv.New64a()
		h.Write([]byte(name(item)))
		if h.Sum64()%uint64(count) == uint64(index) {
			selected = append(selected, item)
		}
	}
	return selected, nil
}

// ShardByWeight selects one index/count partition of items by a greedy
// longest-processing-time partition, as scripts/shard-tests does. Items go
// in order of falling weight, then name, each to the partition with the
// least load, then the lowest index. An item that weights does not name
// weighs 1. The partition of an item depends only on the items that weigh at
// least as much. Thus a heavy item, whose cached verdict costs the most to
// make again, stays in its partition when light items join or leave the list.
// The selected items keep their order in items. An empty selector retains
// the complete list.
func ShardByWeight[T any](items []T, selector string, name func(T) string, weights map[string]int) ([]T, error) {
	if selector == "" {
		return items, nil
	}
	index, count, err := parseSelector(selector)
	if err != nil {
		return nil, err
	}
	weight := func(item T) int {
		if w, ok := weights[name(item)]; ok {
			return w
		}
		return 1
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		wa, wb := weight(items[order[a]]), weight(items[order[b]])
		if wa != wb {
			return wa > wb
		}
		return name(items[order[a]]) < name(items[order[b]])
	})
	loads := make([]int, count)
	owner := make([]int, len(items))
	for _, i := range order {
		least := 0
		for p := 1; p < count; p++ {
			if loads[p] < loads[least] {
				least = p
			}
		}
		owner[i] = least
		loads[least] += weight(items[i])
	}
	var selected []T
	for i, item := range items {
		if owner[i] == index {
			selected = append(selected, item)
		}
	}
	return selected, nil
}

func parseSelector(selector string) (int, int, error) {
	parts := strings.Split(selector, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid corpus shard %q: want index/count", selector)
	}
	index, indexErr := strconv.Atoi(parts[0])
	count, countErr := strconv.Atoi(parts[1])
	if indexErr != nil || countErr != nil || count < 1 || index < 0 || index >= count {
		return 0, 0, fmt.Errorf("invalid corpus shard %q: require 0 <= index < count", selector)
	}
	return index, count, nil
}

// ReadWeights reads a weights file of scripts/shard-tests: one `name
// seconds` row on each line. It ignores blank lines and lines that start
// with `#`. A name that occurs twice is an error.
func ReadWeights(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	weights := map[string]int{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want `name seconds`, got %q", path, n+1, line)
		}
		w, err := strconv.Atoi(fields[1])
		if err != nil || w < 1 {
			return nil, fmt.Errorf("%s:%d: weight %q is not a positive integer", path, n+1, fields[1])
		}
		if _, dup := weights[fields[0]]; dup {
			return nil, fmt.Errorf("%s:%d: %s has a second weight", path, n+1, fields[0])
		}
		weights[fields[0]] = w
	}
	return weights, nil
}

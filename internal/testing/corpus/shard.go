package corpus

import (
	"fmt"
	"hash/fnv"
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
	parts := strings.Split(selector, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid corpus shard %q: want index/count", selector)
	}
	index, indexErr := strconv.Atoi(parts[0])
	count, countErr := strconv.Atoi(parts[1])
	if indexErr != nil || countErr != nil || count < 1 || index < 0 || index >= count {
		return nil, fmt.Errorf("invalid corpus shard %q: require 0 <= index < count", selector)
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

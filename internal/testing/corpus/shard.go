package corpus

import (
	"fmt"
	"strconv"
	"strings"
)

// Shard selects one index/count partition from an ordered corpus.
// An empty selector retains the complete corpus.
func Shard[T any](files []T, selector string) ([]T, error) {
	if selector == "" {
		return files, nil
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
	for i, file := range files {
		if i%count == index {
			selected = append(selected, file)
		}
	}
	return selected, nil
}

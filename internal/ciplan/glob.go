package ciplan

import (
	"regexp"
	"strings"
	"sync"
)

func MatchesAny(patterns, files []string) bool {
	for _, pattern := range patterns {
		for _, file := range files {
			if Match(pattern, file) {
				return true
			}
		}
	}
	return false
}

var patterns sync.Map

func Match(pattern, file string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == file
	}
	if cached, ok := patterns.Load(pattern); ok {
		return cached.(*regexp.Regexp).MatchString(file)
	}
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		case pattern[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteByte('$')
	compiled := regexp.MustCompile(b.String())
	patterns.Store(pattern, compiled)
	return compiled.MatchString(file)
}

package diag

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed explanations/*.md
var explanations embed.FS

func Explain(code string) string {
	if code == "" {
		return ""
	}
	canonical := strings.ToUpper(strings.TrimSpace(code))
	data, err := explanations.ReadFile("explanations/" + canonical + ".md")
	if err != nil {
		return ""
	}
	return string(data)
}

func AvailableCodes() []string {
	entries, err := explanations.ReadDir("explanations")
	if err != nil {
		return nil
	}
	var codes []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		codes = append(codes, strings.TrimSuffix(name, ".md"))
	}
	return codes
}

func FormatExplain(code, body string) string {
	if body == "" {
		return ""
	}
	canonical := strings.ToUpper(strings.TrimSpace(code))
	header := paint(ansiRedBld, "error "+canonical+":")
	if first, rest, ok := strings.Cut(body, "\n"); ok {
		if title, found := strings.CutPrefix(first, "# "+canonical+" — "); found {
			header += " " + title
			body = strings.TrimLeft(rest, "\n")
		}
	}
	return fmt.Sprintf("%s\n\n%s\n", header, body)
}

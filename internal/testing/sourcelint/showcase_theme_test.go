package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The showcase, the fractal explorer, and the source browser each copy the
// documentation theme's palette. This test keeps every copy equal to
// site/src/styles/fern.css, so a palette change there fails here until the
// copies follow it.

var themeDecl = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*(light-dark\(\s*(#[0-9a-fA-F]{3,8})\s*,\s*(#[0-9a-fA-F]{3,8})\s*\)|#[0-9a-fA-F]{3,8}|var\(\s*--[a-z0-9-]+\s*\))`)

// themeTokens reads declarations into name → value, keeping the first one.
func themeTokens(css string) map[string]string {
	out := map[string]string{}
	for _, m := range themeDecl.FindAllStringSubmatch(css, -1) {
		if _, seen := out[m[1]]; !seen {
			out[m[1]] = m[2]
		}
	}
	return out
}

func resolveToken(tokens map[string]string, name string) string {
	for i := 0; i < 8; i++ {
		v, ok := tokens[name]
		if !ok {
			return ""
		}
		if !strings.HasPrefix(v, "var(") {
			return strings.ToLower(v)
		}
		name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "var("), ")"))
	}
	return ""
}

// docsPalette returns the documentation theme's light and dark values.
func docsPalette(t *testing.T, css string) (light, dark map[string]string) {
	const marker = `:root[data-theme="light"] {`
	at := strings.Index(css, marker)
	if at < 0 {
		t.Fatal("fern.css has no light-theme block; update this test with the new shape")
	}
	end := strings.Index(css[at:], "}")
	block := css[at : at+end]
	dark = themeTokens(css[:at] + css[at+end:])
	light = map[string]string{}
	for k, v := range dark {
		light[k] = v
	}
	for k, v := range themeTokens(block) {
		light[k] = v
	}
	return light, dark
}

// themeName maps a copy's token to the documentation token for each mode.
type themeName struct{ light, dark string }

func same(name string) themeName { return themeName{name, name} }

func TestShowcaseThemeMatchesDocs(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	docLight, docDark := docsPalette(t, read("site/src/styles/fern.css"))

	sl := func(n string) themeName { return same("--sl-color-" + n) }
	shared := map[string]themeName{
		"--fern-forest": same("--fern-forest"), "--fern-pine": same("--fern-pine"),
		"--fern-green": same("--fern-green"), "--fern-frond": same("--fern-frond"),
		"--fern-mint": same("--fern-mint"), "--fern-paper": same("--fern-paper"),
		"--fern-bark": same("--fern-bark"), "--fern-spore": same("--fern-spore"),
		"--fern-spore-soft": same("--fern-spore-soft"),
		"--accent-low":      sl("accent-low"), "--accent": sl("accent"), "--accent-high": sl("accent-high"),
		"--text-accent": {"--sl-color-accent", "--sl-color-accent-high"},
		"--white":       sl("white"), "--black": sl("black"), "--hairline": sl("hairline"),
		"--bg-nav": sl("bg-nav"), "--bg-raised": sl("bg-sidebar"),
	}
	for i := 1; i <= 7; i++ {
		n := "--gray-" + string(rune('0'+i))
		shared[n] = sl("gray-" + string(rune('0'+i)))
	}

	copies := []struct {
		file     string
		darkOnly bool
		names    map[string]themeName
	}{
		{"examples/showcase/web/shared/showcase.css", false, shared},
		{"examples/fractal/web/style.css", true, map[string]themeName{
			"--bg-raised": sl("bg-sidebar"), "--hairline": sl("hairline"), "--white": sl("white"),
			"--gray-2": sl("gray-2"), "--gray-3": sl("gray-3"), "--gray-5": sl("gray-5"),
			"--accent": sl("accent"), "--accent-low": sl("accent-low"), "--accent-high": sl("accent-high"),
			"--fern-frond": same("--fern-frond"), "--fern-spore": same("--fern-spore"),
			"--fern-spore-soft": same("--fern-spore-soft"),
		}},
		{"examples/fractal/source.fern", false, map[string]themeName{
			"--bg": sl("black"), "--nav": sl("bg-nav"), "--raised": sl("bg-sidebar"),
			"--line": sl("hairline"), "--text": sl("gray-2"), "--strong": sl("white"),
			"--muted": sl("gray-3"), "--accent": {"--sl-color-accent", "--sl-color-accent-high"},
			"--accent-low": sl("accent-low"), "--spore": same("--fern-spore"),
		}},
	}
	for _, c := range copies {
		tokens := themeTokens(read(c.file))
		for name, doc := range c.names {
			v, ok := tokens[name]
			if !ok {
				t.Errorf("%s: no %s declaration", c.file, name)
				continue
			}
			wantLight, wantDark := resolveToken(docLight, doc.light), resolveToken(docDark, doc.dark)
			if wantLight == "" || wantDark == "" {
				t.Errorf("fern.css: cannot resolve %s / %s", doc.light, doc.dark)
				continue
			}
			gotLight, gotDark := strings.ToLower(v), strings.ToLower(v)
			if m := themeDecl.FindStringSubmatch(name + ": " + v); m != nil && m[3] != "" {
				gotLight, gotDark = strings.ToLower(m[3]), strings.ToLower(m[4])
			}
			if gotDark != wantDark {
				t.Errorf("%s: %s dark value is %s; fern.css has %s", c.file, name, gotDark, wantDark)
			}
			if !c.darkOnly && gotLight != wantLight {
				t.Errorf("%s: %s light value is %s; fern.css has %s", c.file, name, gotLight, wantLight)
			}
		}
	}
}

package modload

import "testing"

func TestManglePrefixesDisambiguateStdlibModules(t *testing.T) {
	const entry = "/project/main.fern"
	paths := map[string]string{
		entry:                             "main",
		"stdlib://std/http/h2/frame.fern": "frame",
		"stdlib://std/http/h3/frame.fern": "frame",
		"stdlib://std/frame_1.fern":       "frame_1",
		"/project/frame.fern":             "frame",
	}
	loaded := map[string]*module{}
	for path, name := range paths {
		loaded[path] = &module{path: path, name: name}
	}
	assignManglePrefixes(loaded, entry)
	seen := map[string]string{}
	for path, m := range loaded {
		if other, exists := seen[m.manglePrefix]; exists {
			t.Errorf("%s and %s both have prefix %q", path, other, m.manglePrefix)
		}
		seen[m.manglePrefix] = path
	}
	for path, want := range map[string]string{
		entry:                             "",
		"stdlib://std/http/h2/frame.fern": "frame__",
		"stdlib://std/http/h3/frame.fern": "frame_2__",
		"stdlib://std/frame_1.fern":       "frame_1__",
		"/project/frame.fern":             "frame_3__",
	} {
		if got := loaded[path].manglePrefix; got != want {
			t.Errorf("%s prefix = %q, want %q", path, got, want)
		}
	}
}

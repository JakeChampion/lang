package checker

import (
	"strings"
	"testing"
)

// An empty map takes its key and value types from the first use that gives
// them. A call of insert or get_or gives them from its arguments. A map that
// no use gives them is one E020 at the `let`, which names the map and not its
// type parameters.
func TestOpenMapLocalTakesTheTypeOfItsUse(t *testing.T) {
	const size = "import \"core/map\";\nfunction size(m: Map[string, i64]): i32 { return m.len(); }\n"
	good := []struct{ name, src string }{
		{"argument", size + "function main(): i32 { let m = Map { }; return size(m); }\n"},
		{"argument then method", size + "function main(): i32 { let m = Map { }; let n = size(m); m = m.insert(\"a\", 5000000000); return n; }\n"},
		{"return", "import \"core/map\";\nfunction fresh(): Map[string, i32] { let m = map_new(8); return m; }\nfunction main(): i32 { return fresh().len(); }\n"},
		{"insert", "import \"core/map\";\nfunction main(): i32 { let m = Map { }; m = m.insert(1, 2); return m.len(); }\n"},
		{"method before the use", size + "function main(): i32 { let m = Map { }; let n = m.len(); return n + size(m); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, src string }{
		{"no use gives the types", "import \"core/map\";\nfunction main(): i32 { let m = Map { }; return m.len(); }\n"},
		{"no use", "import \"core/map\";\nfunction main(): i32 { let m = map_new(8); return 0; }\n"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		msg := err.Error()
		if strings.Count(msg, "type error") != 1 || !strings.Contains(msg, `map "m" needs a type annotation`) || strings.Contains(msg, "K") {
			t.Errorf("%s: want one E020 that names the map, got:\n%s", c.name, msg)
		}
	}
}

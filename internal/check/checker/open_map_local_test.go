package checker

import (
	"strings"
	"testing"
)

// An empty map takes its key and value types from the first use that gives
// them, also through a generic call such as `id(Map { })`. A call of insert or
// get_or gives them from its arguments. A map that no use gives them is one
// E020 at the `let`, which names the map and not its type parameters.
func TestOpenMapLocalTakesTheTypeOfItsUse(t *testing.T) {
	const size = "import \"core/map\";\nfunction size(m: Map[string, i64]): i32 { return m.len(); }\n"
	const ident = "import \"core/map\";\nfunction id[T](x: T): T { return x; }\n"
	good := []struct{ name, src string }{
		{"argument", size + "function main(): i32 { let m = Map { }; return size(m); }\n"},
		{"argument then method", size + "function main(): i32 { let m = Map { }; let n = size(m); m = m.insert(\"a\", 5000000000); return n; }\n"},
		{"return", "import \"core/map\";\nfunction fresh(): Map[string, i32] { let m = map_new(8); return m; }\nfunction main(): i32 { return fresh().len(); }\n"},
		{"insert", "import \"core/map\";\nfunction main(): i32 { let m = Map { }; m = m.insert(1, 2); return m.len(); }\n"},
		{"insert in a range loop", "import \"core/map\";\nfunction main(): i32 { let m = Map { }; for i in 0..4 { m = m.insert(i * 7, i); } return m.len(); }\n"},
		{"method before the use", size + "function main(): i32 { let m = Map { }; let n = m.len(); return n + size(m); }\n"},
		{"through a generic call", ident + "function main(): i32 { let m = id(Map { }); let n: Map[string, i32] = m; return n.len(); }\n"},
		{"insert through a generic call", ident + "function main(): i32 { let m = id(Map { }); m = m.insert(\"a\", 1); return m.len(); }\n"},
		{"generic call as an argument", ident + "function size(m: Map[string, i64]): i32 { return m.len(); }\nfunction main(): i32 { return size(id(Map { })); }\n"},
		{"generic call as a statement", ident + "function main(): i32 { id(Map { }); return 0; }\n"},
		{"if arm beside a typed map", "import \"core/map\";\nfunction main(): i32 { let m: Map[i32, i32] = if (true) { Map { } } else { Map { 1: 2 } }; return m.len(); }\n"},
		{"else arm beside a typed map", "import \"core/map\";\nfunction main(): i32 { let n = if (false) { Map { 3: 4 } } else { Map { } }; let k: Map[i32, i32] = n; return k.len(); }\n"},
		{"match arm beside a typed map", "import \"core/map\";\nfunction main(): i32 { let b = true; let m = match (b) { true => Map { }, _ => Map { 1: 2 } }; return m.len(); }\n"},
		{"array element beside a typed map", "import \"core/map\";\nfunction main(): i32 { let ms = [Map { }, Map { 1: 2 }]; return ms[0].len(); }\n"},
		{"empty first if arm", "import \"core/map\";\nfunction main(): i32 { let m = if (false) { Map { } } else { Map { 1: 2 } }; return m.len(); }\n"},
		{"empty if arm in a return", "import \"core/map\";\nfunction f(b: boolean): Map[string, i32] { return if (b) { Map { } } else { Map { \"a\": 1 } }; }\nfunction main(): i32 { return f(true).len(); }\n"},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, src string }{
		{"no use gives the types", "import \"core/map\";\nfunction main(): i32 { let m = Map { }; return m.len(); }\n"},
		{"no use", "import \"core/map\";\nfunction main(): i32 { let m = map_new(8); return 0; }\n"},
		{"no use through a generic call", ident + "function main(): i32 { let m = id(Map { }); return 0; }\n"},
		{"no arm gives the types", "import \"core/map\";\nfunction main(): i32 { let m = if (true) { Map { } } else { Map { } }; return m.len(); }\n"},
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

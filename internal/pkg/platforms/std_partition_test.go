package platforms

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/stdlib"
)

var stdModuleReach = map[string]string{
	"std/_test_empty":   "",
	"std/ansi":          "",
	"std/array":         "",
	"std/async":         "now,reactor,tcp,uring",
	"std/base32":        "",
	"std/base64":        "",
	"std/bench":         "now",
	"std/binary":        "",
	"std/brotli":        "",
	"std/cli":           "env",
	"std/codec":         "",
	"std/constmap":      "",
	"std/convert":       "",
	"std/crypto":        "",
	"std/csv":           "",
	"std/deflate":       "",
	"std/dns":           "fs,host,now,random,reactor,tcp,unix,uring",
	"std/dotenv":        "",
	"std/envflag":       "env",
	"std/errno":         "",
	"std/error":         "",
	"std/fetch":         "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/f64":           "",
	"std/format":        "",
	"std/fuzz":          "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/glob":          "",
	"std/hash":          "",
	"std/headers":       "",
	"std/hex":           "",
	"std/http":          "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/i32":           "",
	"std/i64":           "",
	"std/io":            "fs,stdin",
	"std/io_buffered":   "",
	"std/io_outcome":    "",
	"std/jni":           "cabi",
	"std/json":          "",
	"std/log":           "log",
	"std/math":          "random",
	"std/metrics":       "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/mock_platform": "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/net":           "now,reactor,tcp,unix,uring",
	"std/ndarray":       "",
	"std/num":           "",
	"std/option":        "",
	"std/ordmap":        "",
	"std/ordset":        "",
	"std/parse":         "",
	"std/path":          "",
	"std/peg":           "",
	"std/platform":      "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/pmap":          "",
	"std/png":           "",
	"std/protobuf":      "",
	"std/pset":          "",
	"std/pvec":          "",
	"std/publicsuffix":  "",
	"std/rand":          "random",
	"std/regex":         "",
	"std/result":        "",
	"std/semver":        "",
	"std/serve":         "config,env,fs,host,ktls,log,now,proc,random,reactor,sysinfo,tcp,unix,uring",
	"std/set":           "",
	"std/signal":        "signal",
	"std/sim":           "now,random,reactor,tcp,uring",
	"std/sim_fetch":     "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/sim_platform":  "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/simd":          "",
	"std/strdist":       "",
	"std/stream":        "",
	"std/string":        "",
	"std/table":         "",
	"std/test":          "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/textwrap":      "",
	"std/time":          "now",
	"std/tls":           "env,fs,host,now,random,reactor,tcp,unix,uring",
	"std/tz":            "env,fs,now",
	"std/u32":           "",
	"std/u64":           "",
	"std/unicode":       "",
	"std/url":           "",
	"std/utf8":          "",
	"std/uuid":          "now,random",
	"std/wasi_http":     "config,env,fs,host,ktls,log,now,random,reactor,tcp,unix,uring",
	"std/zstd":          "",
}

func TestStdPartitionIsDerivedNotAsserted(t *testing.T) {
	ents, err := fs.ReadDir(stdlib.FS(), "std")
	if err != nil {
		t.Fatalf("read embedded std/: %v", err)
	}
	found := map[string]bool{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".fern") {
			continue
		}
		mod := "std/" + strings.TrimSuffix(name, ".fern")
		found[mod] = true
		t.Run(mod, func(t *testing.T) {
			prog, err := modload.LoadStdlibFlat([]string{mod})
			if err != nil {
				t.Fatalf("cannot load %s, so it cannot be classified: %v", mod, err)
			}
			var caps []string
			for c := range Reach(prog) {
				caps = append(caps, c)
			}
			sort.Strings(caps)
			got := strings.Join(caps, ",")

			want, listed := stdModuleReach[mod]
			if !listed {
				t.Fatalf("%s is not in stdModuleReach; its derived reach is %q — add it", mod, got)
			}
			if got != want {
				t.Errorf("%s reach = %q, table says %q\n"+
					"A module changing which host capabilities it can reach is exactly what this table exists to surface. "+
					"If the change is intended, update the entry; if not, the import that pulled it in is the bug.",
					mod, got, want)
			}
		})
	}
	for mod := range stdModuleReach {
		if !found[mod] {
			t.Errorf("stdModuleReach lists %s, which no longer exists in std/ — drop it", mod)
		}
	}
}

func TestStdCoreSafeModulesCheckAgainstFreestanding(t *testing.T) {
	for mod, reach := range stdModuleReach {
		if reach != "" {
			continue
		}
		t.Run(mod, func(t *testing.T) {
			prog, err := modload.LoadStdlibFlat([]string{mod})
			if err != nil {
				t.Fatalf("load %s: %v", mod, err)
			}
			if v := Enforce(prog, "freestanding"); len(v) != 0 {
				t.Errorf("%s is classified core-safe but E066s on freestanding: %s", mod, v[0].Message(""))
			}
		})
	}
}

package e2eharness

import (
	"fmt"
	"strings"
	"testing"
)

func TestHTTPHandlerCensusThreadsBoundAcrossBurst(t *testing.T) {
	for _, wasi := range []bool{false, true} {
		for _, rounds := range []int{32, KeepAliveCycle} {
			t.Run(fmt.Sprintf("wasi=%t/rounds=%d", wasi, rounds), func(t *testing.T) {
				var source string
				if wasi {
					source = WasiHTTPHandlerCensusSource(t, "../../..", rounds)
				} else {
					source = HTTPHandlerCensusSource(t, "../../..", rounds)
				}
				loopStart := strings.Index(source, "function __serve_loop(")
				burstStart := strings.Index(source, "function __serve_burst[")
				burstEnd := strings.Index(source, "\n// __serve_loop_with")
				if loopStart < 0 || burstStart <= loopStart || burstEnd <= burstStart {
					t.Fatal("bounded production loop/burst boundaries missing")
				}
				loop, burst := source[loopStart:burstStart], source[burstStart:burstEnd]
				for _, text := range []string{
					"let work: Option[i32] = None;",
					"work = Some(20000000);",
					"work = Some(100000000);",
					"census_burn(work.unwrap_or(0), plat)",
				} {
					if strings.Count(source, text) != 1 {
						t.Errorf("fixture must exercise optional clock delay through %q", text)
					}
				}
				for _, text := range []string{
					"let completed: i32 = 0;",
					fmt.Sprintf("while (completed < %d || conns.open > 0)", rounds),
					"let burst: (__Conns, __Flight[], i32) = __serve_burst(drv, conns, flights, at, ev.2, handler, start, opts, stop, clk, completed);",
					"completed = burst.2;",
				} {
					if strings.Count(loop, text) != 1 {
						t.Errorf("bounded loop must contain exactly one %q", text)
					}
				}
				for _, text := range []string{
					"census_completed: i32): (__Conns, __Flight[], i32)",
					"let completed: i32 = census_completed;",
					"more = answered.1;\n                        completed = completed + 1;",
					"return (conns, flights, completed);",
				} {
					if strings.Count(burst, text) != 1 {
						t.Errorf("bounded burst must contain exactly one %q", text)
					}
				}
			})
		}
	}
}

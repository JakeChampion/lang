package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Run the same independent state/protocol assertions against each representation.
// Only field access changes; model answers and allocation assertions stay intact.
func prepareBrokerRepresentation(t *testing.T, dir, source string, ring bool) string {
	t.Helper()
	name := "broker_core.fern"
	if ring {
		name = "broker_ring.fern"
		source = "import \"./bounded_ring\";\n" + source
		source = strings.ReplaceAll(source, "b.queues[b.heads[0]]", "bounded_ring.front_or(b.queues[0], -1)")
		source = strings.ReplaceAll(source, "b.queues[(b.heads[0] + i) % b.queue_capacity]", "bounded_ring.at_or(b.queues[0], i, -1)")
		source = strings.ReplaceAll(source, "b.queues[s*b.queue_capacity+(b.heads[s]+i)%b.queue_capacity]", "bounded_ring.at_or(b.queues[s],i,-1)")
		source = regexp.MustCompile(`(b|saved)\.lengths\[([^\]]+)\]`).ReplaceAllString(source, "$1.queues[$2].count")
		source = strings.ReplaceAll(source, "max.queues.len() != 65536", "max.queues.len() != 16 || !all_queue_capacities(max,4096)")
		source += "\nfunction all_queue_capacities(b:broker_core.Broker,capacity:i32):boolean {let i:i32=0;while(i<b.subscribers){if(bounded_ring.capacity(b.queues[i])!=capacity){return false;}i=i+1;}return true;}\n"
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "bounded_ring.fern"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bounded_ring.fern"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broker_core.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

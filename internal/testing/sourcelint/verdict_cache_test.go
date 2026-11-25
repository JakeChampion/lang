package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A job that restores the oracle verdicts must save them as its last step,
// also when the job fails. The post step of actions/cache saves only after a
// pass, so a cold job that times out would save nothing, and each later run
// would start as cold and time out again.
func TestVerdictCacheJobsSaveWhenTheyFail(t *testing.T) {
	header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):\s*$`)
	step := regexp.MustCompile(`(?m)^      - `)
	const want = "- uses: ./.github/actions/oracle-verdict-save\n        if: always()"
	checked := 0
	for _, file := range workflowFiles(t) {
		body, ok := topLevelBlock(workflowSource(t, file), "jobs")
		if !ok {
			continue
		}
		idx := header.FindAllStringSubmatchIndex(body, -1)
		for n, m := range idx {
			id := body[m[2]:m[3]]
			end := len(body)
			if n+1 < len(idx) {
				end = idx[n+1][0]
			}
			job := body[m[0]:end]
			if !strings.Contains(job, "uses: ./.github/actions/oracle-verdict-cache") {
				continue
			}
			checked++
			steps := step.FindAllStringIndex(job, -1)
			var kept []string
			for _, ln := range strings.Split(job[steps[len(steps)-1][0]:], "\n") {
				if !strings.HasPrefix(strings.TrimSpace(ln), "#") {
					kept = append(kept, ln)
				}
			}
			last := strings.TrimSpace(strings.Join(kept, "\n"))
			if last != want {
				t.Errorf("%s: job %q restores the oracle verdicts, but its last step is not\n%s\nso a failed or timed-out job keeps none of the verdicts it computed", file, id, want)
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d job(s) restore the oracle verdicts; did the e2e lanes' shape change?", checked)
	}

	actions := filepath.Join("..", "..", "..", ".github", "actions")
	for _, c := range []struct{ action, needle, why string }{
		{"oracle-verdict-cache", "uses: actions/cache/restore@", "the restore must not carry the post step of actions/cache, which saves only after a pass"},
		{"oracle-verdict-cache", "ORACLE_VERDICT_KEY=", "oracle-verdict-save reads the key from the environment"},
		{"oracle-verdict-save", "uses: actions/cache/save@", "this step is what saves the verdicts"},
		{"oracle-verdict-save", "key: ${{ env.ORACLE_VERDICT_KEY }}", "the save must use the key of the restore"},
	} {
		src, err := os.ReadFile(filepath.Join(actions, c.action, "action.yml"))
		if err != nil {
			t.Fatalf("read %s: %v", c.action, err)
		}
		if !strings.Contains(string(src), c.needle) {
			t.Errorf("%s/action.yml no longer contains %q — %s", c.action, c.needle, c.why)
		}
	}
}

package sourcelint

import (
	"regexp"
	"strings"
	"testing"
)

// Every job that runs tests through gotestsum or cmd/ci-test-workers ends its
// test steps with scripts/ci-annotate-failures on the event streams they
// wrote, so a failed test's name and output reach the Checks tab and the
// REST API, where the job log does not. One step per job, after the last
// test step: a step per test step would annotate the same stream twice.
func TestTestRunningJobsAnnotateTheirFailures(t *testing.T) {
	header := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_-]+):\s*$`)
	workerOutput := regexp.MustCompile(`-output "\$RUNNER_TEMP/([^"]+)"`)
	annotate := regexp.MustCompile(`(?m)^      - name: name the failed tests as annotations\n        if: failure\(\)\n        run: scripts/ci-annotate-failures (.*)$`)
	checked := 0
	for _, file := range workflowFiles(t) {
		src := workflowSource(t, file)
		body, ok := topLevelBlock(src, "jobs")
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
			direct := strings.Contains(job, "gotestsum --format")
			outputs := workerOutput.FindAllStringSubmatch(job, -1)
			if !direct && len(outputs) == 0 {
				continue
			}
			checked++
			steps := annotate.FindAllStringSubmatch(job, -1)
			if len(steps) != 1 {
				t.Errorf("%s: job %q runs tests but has %d failure-annotation step(s); want exactly one, after its last test step", file, id, len(steps))
				continue
			}
			args := steps[0][1]
			if direct {
				if !strings.Contains(args, `"$GOTESTSUM_JSONFILE"`) {
					t.Errorf("%s: job %q runs gotestsum directly but its annotation step does not read \"$GOTESTSUM_JSONFILE\"", file, id)
				}
				if !strings.Contains(src, "GOTESTSUM_JSONFILE: /tmp/gotestsum.jsonl") {
					t.Errorf("%s: a job runs gotestsum directly but the workflow does not set GOTESTSUM_JSONFILE, so gotestsum writes no event stream to annotate from", file)
				}
			}
			for _, o := range outputs {
				want := `"$RUNNER_TEMP"/` + o[1] + `/worker-*.jsonl`
				if !strings.Contains(args, want) {
					t.Errorf("%s: job %q runs ci-test-workers into %s but its annotation step does not read %s", file, id, o[1], want)
				}
			}
			// The step must come after every test step, or a failure in a
			// later one is never annotated.
			at := strings.Index(job, steps[0][0])
			rest := job[at+len(steps[0][0]):]
			if strings.Contains(rest, "gotestsum --format") || workerOutput.MatchString(rest) {
				t.Errorf("%s: job %q runs tests after its failure-annotation step; move the step after the last test step", file, id)
			}
		}
	}
	if checked < 15 {
		t.Fatalf("only %d test-running job(s) found across the workflows; did the lanes' shape change?", checked)
	}
}

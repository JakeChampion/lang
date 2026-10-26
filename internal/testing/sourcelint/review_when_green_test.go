package sourcelint

import (
	"strings"
	"testing"
)

const pullfrogFile = "pullfrog.yml"
const reviewWhenGreenFile = "review-when-green.yml"

// A Pullfrog review runs only once the fast tier is green. The gate in
// pullfrog.yml defers a review whose CI run has not completed, and
// review-when-green.yml dispatches it again when that run completes green.
// The two halves meet on the cache key, so both are pinned here.
func TestPullfrogReviewWaitsForTheFastTier(t *testing.T) {
	gate := workflowSource(t, pullfrogFile)
	for _, want := range []struct{ needle, why string }{
		{`workflow_id: "ci.yml", event: "pull_request", head_sha: pr.head.sha`, "the gate reads the head's pull-request CI run"},
		{`if (!ci || ci.status !== "completed") {`, "a head whose tier has not reported is deferred, not reviewed"},
		{`core.setOutput("defer", "true");`, "the deferral is an output the save steps key on"},
		{"core.setOutput(\"cache_key\", `pullfrog-deferred-${pr.head.sha}-${context.runId}`);", "the saved inputs are keyed by the head, which is all the dispatcher knows"},
		{`if (ci.conclusion !== "success") {`, "a red head gets no review"},
		{`if: steps.gate.outputs.defer == 'true'`, "the inputs are saved only for a deferred review"},
		{`uses: actions/cache/save@v6`, "the inputs travel through the Actions cache"},
	} {
		if !strings.Contains(gate, want.needle) {
			t.Errorf("%s lacks %q: %s", pullfrogFile, want.needle, want.why)
		}
	}

	dispatcher := workflowSource(t, reviewWhenGreenFile)
	for _, want := range []struct{ needle, why string }{
		{"workflow_run:\n    workflows: [\"CI\"]\n    types: [completed]", "the dispatcher runs when a CI run completes, so the gate's completed check holds on the re-dispatch"},
		{`if: github.event.workflow_run.event == 'pull_request' && github.event.workflow_run.conclusion == 'success'`, "only a green pull-request run dispatches a review"},
		{`key: pullfrog-deferred-${{ github.event.workflow_run.head_sha }}-`, "the restore key is the prefix of the key the gate saves under"},
		{`if: steps.deferred.outputs.cache-matched-key != ''`, "a head with nothing deferred dispatches nothing"},
		{`workflow_id: "pullfrog.yml"`, "the deferred review is the Pullfrog workflow itself"},
	} {
		if !strings.Contains(dispatcher, want.needle) {
			t.Errorf("%s lacks %q: %s", reviewWhenGreenFile, want.needle, want.why)
		}
	}
	jobs := workflowJobs(t, reviewWhenGreenFile)
	if len(jobs) != 1 || jobs[0].id != "dispatch" {
		t.Errorf("%s has jobs %v; one `dispatch` job restores and dispatches", reviewWhenGreenFile, jobs)
	}
}

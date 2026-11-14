import json
import os
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "merge-queue")

FAKE_GH = r'''#!/usr/bin/env python3
import json, os, sys
world = json.load(open(os.environ["FAKE_GH_WORLD"]))
args = sys.argv[1:]
with open(os.environ["FAKE_GH_LOG"], "a") as f:
    f.write(json.dumps(args) + "\n")
if args[:2] == ["pr", "list"]:
    print(json.dumps(world["prs"]))
elif args[0] == "api":
    sha = args[1].split("/commits/")[1].split("/")[0]
    runs = [{"conclusion": "success"}] if sha in world["green"] else []
    print(json.dumps({"check_runs": runs}))
'''


class MergeQueueTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.remote = os.path.join(self.tmp, "remote.git")
        self.work = os.path.join(self.tmp, "work")
        self.world = os.path.join(self.tmp, "world.json")
        self.log = os.path.join(self.tmp, "gh.log")
        fake = os.path.join(self.tmp, "gh")
        with open(fake, "w") as f:
            f.write(FAKE_GH)
        os.chmod(fake, 0o755)
        self.env = {
            **{k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "GITHUB_", "MERGE_QUEUE_"))},
            "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@fern.invalid",
            "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@fern.invalid",
            "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull,
            "MERGE_QUEUE_GH": fake, "FAKE_GH_WORLD": self.world, "FAKE_GH_LOG": self.log,
        }
        self.git(self.tmp, "init", "-q", "--bare", "-b", "main", self.remote)
        self.git(self.tmp, "clone", "-q", self.remote, self.work)
        self.write("base.txt", "base\n")
        os.makedirs(os.path.join(self.work, ".github"))
        self.write(".github/perf-baseline.txt", "x86_64/a.ir\t1\n")
        self.git(self.work, "checkout", "-q", "-b", "main")
        self.commit("first")
        self.git(self.work, "push", "-q", "origin", "main")
        self.prs = []
        self.green = []

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def git(self, cwd, *args, check=True):
        r = subprocess.run(["git", *args], cwd=cwd, env=self.env, capture_output=True, text=True)
        if check and r.returncode != 0:
            self.fail(f"git {' '.join(args)}: {r.stdout}{r.stderr}")
        return r.stdout.strip()

    def write(self, rel, text):
        with open(os.path.join(self.work, rel), "w") as f:
            f.write(text)

    def commit(self, subject):
        self.git(self.work, "add", "-A")
        self.git(self.work, "commit", "-q", "-m", subject)
        return self.git(self.work, "rev-parse", "HEAD")

    def pull_request(self, number, rel, text, green=True, draft=False, labels=("merge-queue",)):
        self.git(self.work, "checkout", "-q", "-B", f"pr{number}", "origin/main")
        self.write(rel, text)
        sha = self.commit(f"change {number}")
        self.git(self.work, "push", "-q", "-f", "origin", f"HEAD:refs/pull/{number}/head")
        self.prs = [p for p in self.prs if p["number"] != number]
        self.prs.append({"number": number, "headRefOid": sha, "headRefName": f"pr{number}", "isDraft": draft,
                         "labels": [{"name": name} for name in labels]})
        if green:
            self.green.append(sha)
        return sha

    def push_main(self, rel, text):
        self.git(self.work, "fetch", "-q", "origin", "main")
        self.git(self.work, "checkout", "-q", "-B", "main", "origin/main")
        self.write(rel, text)
        self.commit(f"main moves {rel}")
        self.git(self.work, "push", "-q", "origin", "main")

    def queue(self, *args):
        with open(self.world, "w") as f:
            json.dump({"prs": self.prs, "green": self.green}, f)
        open(self.log, "w").close()
        r = subprocess.run([SCRIPT, *args], cwd=self.work, env=self.env, capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        with open(self.log) as f:
            self.calls = [json.loads(line) for line in f]
        # The labels that the script set are the labels of the next call.
        for call in self.called("pr", "edit"):
            pr = next(p for p in self.prs if p["number"] == int(call[2]))
            names = {label["name"] for label in pr["labels"]}
            names = names | {call[4]} if call[3] == "--add-label" else names - {call[4]}
            pr["labels"] = [{"name": name} for name in sorted(names)]
        return r.stdout

    def land(self, result):
        self.git(self.work, "fetch", "-q", "origin", "merge-queue/batch")
        self.git(self.work, "checkout", "-q", "--detach", "FETCH_HEAD")
        return self.queue("land", result)

    def remote_ref(self, ref):
        return self.git(self.remote, "rev-parse", "--verify", "-q", ref, check=False)

    def batch(self):
        subjects = self.git(self.remote, "log", "--first-parent", "--merges", "--reverse", "--format=%s",
                            "main..merge-queue/batch")
        return [int(s.split("#")[1].split()[0]) for s in subjects.splitlines()]

    def state(self):
        return json.loads(self.git(self.remote, "cat-file", "-p", "refs/merge-queue/state"))

    def called(self, *prefix):
        return [c for c in self.calls if c[:len(prefix)] == list(prefix)]

    def test_form_takes_the_green_pull_requests_in_order_and_dispatches(self):
        self.pull_request(3, "c.txt", "c\n")
        self.pull_request(1, "a.txt", "a\n")
        self.pull_request(2, "b.txt", "b\n", green=False)
        self.pull_request(4, "d.txt", "d\n", draft=True)
        self.queue("form")
        self.assertEqual(self.batch(), [1, 3])
        self.assertEqual(self.called("workflow", "run"),
                         [["workflow", "run", "merge-queue-batch.yml", "--ref", "merge-queue/batch"]])
        self.assertEqual(self.called("pr", "edit"), [])

    def test_form_does_nothing_while_a_batch_is_in_flight(self):
        self.pull_request(1, "a.txt", "a\n")
        self.queue("form")
        tip = self.remote_ref("merge-queue/batch")
        self.pull_request(2, "b.txt", "b\n")
        self.queue("form")
        self.assertEqual(self.remote_ref("merge-queue/batch"), tip)
        self.assertEqual(self.called("workflow", "run"), [])

    def test_form_with_nothing_ready_makes_no_batch(self):
        self.pull_request(1, "a.txt", "a\n", green=False)
        self.queue("form")
        self.assertEqual(self.remote_ref("merge-queue/batch"), "")

    def test_a_pull_request_that_conflicts_leaves_the_queue(self):
        self.pull_request(1, "base.txt", "one\n")
        self.pull_request(2, "base.txt", "two\n")
        self.pull_request(3, "c.txt", "c\n")
        self.queue("form")
        self.assertEqual(self.batch(), [1, 3])
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "2", "--remove-label", "merge-queue"]])
        self.assertIn("does not merge cleanly", self.called("pr", "comment")[0][4])

    def test_land_success_moves_main_to_the_tested_commit(self):
        self.pull_request(1, "a.txt", "a\n")
        self.pull_request(2, "b.txt", "b\n")
        self.queue("form")
        tip = self.remote_ref("merge-queue/batch")
        self.land("success")
        self.assertEqual(self.remote_ref("main"), tip)
        self.assertEqual(self.remote_ref("merge-queue/batch"), "")
        self.assertEqual(self.state()["retry"], [])
        self.assertEqual(self.called("workflow", "run"), [["workflow", "run", "merge-queue.yml", "--ref", "main"]])

    def test_a_red_batch_is_split_and_the_first_half_is_tested_alone(self):
        self.pull_request(1, "a.txt", "a\n")
        self.pull_request(2, "b.txt", "b\n")
        self.pull_request(3, "c.txt", "c\n")
        self.queue("form")
        main = self.remote_ref("main")
        self.land("failure")
        self.assertEqual(self.remote_ref("main"), main)
        self.assertEqual(self.state()["retry"], [[1, 2], [3]])
        self.assertEqual(self.called("pr", "edit"), [])
        self.pull_request(4, "d.txt", "d\n")
        self.queue("form")
        self.assertEqual(self.batch(), [1, 2])
        self.assertEqual(self.state()["retry"], [[3]])

    def test_a_red_batch_of_one_removes_that_pull_request(self):
        self.pull_request(1, "a.txt", "a\n")
        self.queue("form")
        report = os.path.join(self.tmp, "failure.md")
        with open(report, "w") as f:
            f.write("### [Lint / lint](https://example.test/job)\n")
        self.env["MERGE_QUEUE_FAILURE_REPORT"] = report
        self.land("failure")
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "1", "--remove-label", "merge-queue"]])
        self.assertIn("the suite failed with only this pull request", self.called("pr", "comment")[0][4])
        self.assertIn("### [Lint / lint](https://example.test/job)", self.called("pr", "comment")[0][4])
        self.assertEqual(self.state()["retry"], [])
        self.assertEqual(self.remote_ref("merge-queue/batch"), "")

    def test_a_cancelled_suite_puts_the_batch_back(self):
        self.pull_request(1, "a.txt", "a\n")
        self.pull_request(2, "b.txt", "b\n")
        self.queue("form")
        self.land("cancelled")
        self.assertEqual(self.state()["retry"], [[1, 2]])
        self.assertEqual(self.called("pr", "edit"), [])

    def test_land_merges_pin_commits_that_main_got(self):
        self.pull_request(1, "a.txt", "a\n")
        self.queue("form")
        self.push_main(".github/perf-baseline.txt", "x86_64/a.ir\t2\n")
        self.land("success")
        self.assertEqual(self.git(self.remote, "show", "main:a.txt"), "a")
        self.assertEqual(self.git(self.remote, "show", "main:.github/perf-baseline.txt"), "x86_64/a.ir\t2")
        self.assertEqual(self.state()["retry"], [])

    def test_land_tests_the_batch_again_when_main_got_other_commits(self):
        self.pull_request(1, "a.txt", "a\n")
        self.queue("form")
        self.push_main("other.txt", "other\n")
        main = self.remote_ref("main")
        self.land("success")
        self.assertEqual(self.remote_ref("main"), main)
        self.assertEqual(self.state()["retry"], [[1]])
        self.queue("form")
        self.assertEqual(self.batch(), [1])
        self.assertEqual(self.git(self.remote, "show", "merge-queue/batch:other.txt"), "other")

    def test_a_green_pull_request_gets_the_label_and_joins_the_batch(self):
        self.pull_request(1, "a.txt", "a\n", labels=())
        self.pull_request(2, "b.txt", "b\n", labels=(), green=False)
        self.pull_request(3, "c.txt", "c\n", labels=(), draft=True)
        self.pull_request(4, "d.txt", "d\n", labels=("merge-queue-hold",))
        self.queue("form")
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "1", "--add-label", "merge-queue"]])
        self.assertEqual(self.batch(), [1])

    def test_a_removed_pull_request_gets_the_label_again_only_with_a_new_commit(self):
        self.pull_request(1, "a.txt", "a\n", labels=())
        self.queue("form")
        self.land("failure")
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "1", "--remove-label", "merge-queue"]])
        self.queue("form")
        self.assertEqual(self.called("pr", "edit"), [])
        self.assertEqual(self.remote_ref("merge-queue/batch"), "")
        self.pull_request(1, "a.txt", "fixed\n", labels=())
        self.queue("form")
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "1", "--add-label", "merge-queue"]])
        self.assertEqual(self.batch(), [1])

    def test_a_person_can_put_a_removed_pull_request_back(self):
        self.pull_request(1, "base.txt", "one\n")
        self.pull_request(2, "base.txt", "two\n")
        self.queue("form")
        self.land("success")
        self.assertEqual(self.state()["removed"], {"2": self.prs[1]["headRefOid"]})
        self.prs = [p for p in self.prs if p["number"] == 2]
        self.prs[0]["labels"] = [{"name": "merge-queue"}]
        self.queue("form")
        self.assertEqual(self.called("pr", "edit"), [["pr", "edit", "2", "--remove-label", "merge-queue"]])


if __name__ == "__main__":
    unittest.main()

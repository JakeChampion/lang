# Agent prompting on this repository

Status: [reference] — living. How the published prompting guidance for the
Claude Fable 5.1 / Mythos 5.1 generation applies to work in this tree: which of
its findings `CLAUDE.md` already encodes, which this repository deliberately
overrides, and the harness-level settings that are not the model's to fix.

Source: [Prompting Claude Fable 5.1](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1)
(read 2026-09-14). Read the source for the full prompt snippets; this doc keeps
the ones worth reusing verbatim and says what to do with them here.

The snippets below are written for a *system prompt* or a *user message* — they
are inputs to an agent, not rules for a human contributor. Copy them into a
harness; do not paraphrase them into `CLAUDE.md`, which is already long enough
that every added line is a tax on every turn.

## Where this repository overrides the guidance

Two of the published defaults are wrong *here*, and the repository wins.

**Scope and tests.** The source recommends telling the model to leave a
pre-existing bug it trips over as a follow-up note, and to commit tests only
where the task or the neighbouring code already asks for them. `CLAUDE.md` says
the opposite on both counts, deliberately: *Fix bugs you find on the way*
(same PR, with its own test), *A Fern quirk or bug you hit gets an issue AND a
fix — never a workaround*, and *Every new feature ships with tests*. Those rules
exist because the alternative — a TODO, a known-divergence row, a follow-up
issue — is exactly the workaround the engineering bar forbids. Do not import the
"keep changes and tests to what the task asks for" instruction into an agent
pointed at this tree; it would suppress the behaviour the project wants.

The part of that section that does survive: implement the reading the task's
wording and the surrounding code most directly support, state the assumption,
and do not build for both readings. Scratch scripts used to verify need not be
committed.

**Asking versus proceeding.** The source's autonomy block carries an exception:
when the user is describing a problem or thinking out loud rather than
requesting a change, the deliverable is the assessment — report and stop. That
exception is worth keeping and is now in `CLAUDE.md`. The rest of that block
(the user is not watching; do not ask permission for work already requested)
restates what `CLAUDE.md` already says at more length.

## Behaviours to watch for, and the fix

Each of these is a way this model generation differs from the previous one. The
first four are summarised in `CLAUDE.md`'s working-style list; the rest live
only here.

**Fewer user-facing updates.** Long tool-calling turns go quiet, and the final
message can cover only the last step. This bites hardest here, where a single
suite can run for 45 minutes. Say what you are about to do, and close with a
recap that stands alone. It compounds with a second fact: the user sees at most
a few lines of a command's output, so anything they need from a test log has to
be *in the reply*, not left in the terminal. A harness can say so directly:

```text
Only you see that command's output — the user's terminal shows at most a few
lines of it. If the user needs to read any of it, put it in your reply.
```

**One tool call per turn in coding loops.** When the next reads are implied by
the task rather than named by the user, the calls can arrive one per turn. Each
extra turn costs a round trip, and on a 4-core container those add up faster
than the work itself. The nudge, sent after each batch of tool results:

```text
First privately list what you need next; then request every item that doesn't
depend on another's result in this one response.
```

**Whole-file rewrites for small edits.** The result is usually the same file,
but on the self-host sources — several of which are thousands of lines — a
rewrite costs output tokens and time, and it makes the diff unreadable, which
matters more. `CLAUDE.md` carries the short form; the source's wording is:

```text
The number of tokens used to edit files is best minimized, all else being
equal. Therefore, when it will not affect the end result, try to surgically
edit a file rather than rewrite the entire thing.
```

**Denser prose.** Sentences run longer, with fewer paragraph breaks, and
metaphor substitutes for direct statement. This shows up in commit messages, PR
bodies and `docs/` — all of which are read by people. The definition of the
anti-pattern works better than an instruction to be brief:

```text
Mannered prose substitutes metaphor and flourish for direct statement. Instead
of "a parameter worth varying," the mannered writer produces "a dial worth
turning." Instead of "this point still matters," they write "this point earns
its keep." The phrases exist to display the writer, not to convey the idea, and
readers can tell. That is why mannered prose irritates: it makes the reader work
harder so the writer can perform. It is also imprecise. Metaphors drag in
connotations the writer did not choose and cannot control. The fix is to say
what you mean. When a literal phrase is available, use it.
```

"Please remove all mannered prose" usually does the same job.

**Less formatting in chat, not more.** Prompts written against older models
often carry anti-bullet, anti-header rules. This generation already under-uses
them; such a rule now makes replies worse. Delete it, or replace it with one
that says when structure *is* wanted.

**Search skipped at low effort.** At `low`, the model answers from memory where
it would previously have searched. That is the failure mode `CLAUDE.md` already
warns about for issue trackers — #4451 / #4363 / #4346 all described work that
was already done — generalised: recognising a name is not knowing its current
state. Raising effort for the affected turns is often the cleaner fix than a
prompt change. Where a prompt change is wanted:

```text
When a query centers on a name you do not confidently recognize, or recognize
from a fast-moving area like AI models and developer tools where the landscape
shifts within months, the name itself is the thing to verify: search before
answering, and include the name as the user wrote it in at least one query
alongside any reformulations. This holds even when you have some background on
it — partial background is exactly what makes an out-of-date answer sound
authoritative, so familiarity is not a reason to skip the search.
```

**Safeguard false positives.** A blocked request comes back as
`stop_reason: "refusal"`. Three triggers, two of which this project meets
routinely:

- *Compile-check phrasing.* "Does this program compile without errors?" is more
  likely to trip the classifier than "Are there any bugs in this program?" —
  ask the second.
- *Lesser-known languages.* Fern is exactly that: a language with no public
  corpus behind it. Give the model the language's own documentation — `spec/`,
  `docs/LANGUAGE-DIRECTION.md`, the stdlib sources — rather than a bare snippet
  and a question.
- *Base64 in tool output.* Tools that pipe base64 into context raise the false
  positive rate; strip it at the tool boundary.

Finding vulnerabilities in source code is permitted, and the false-positive rate
is lower than the previous generation's at launch.

**Unmarked quotation when summarising sources.** Retrieved passages can come
back reproduced rather than reworded. The fix is one complete worked example in
the system prompt — request, response, and a sentence saying why that response
is correct. Low-frequency here; the source has the example.

## Harness-level settings

None of these are fixed by editing `CLAUDE.md`; they belong to whatever runs the
agent.

**Effort.** `high` is the default and the right starting point. Sweep the other
levels (`low`, `medium`, `xhigh`, `max`) against real tasks — the level names do
not correspond to the same amount of thinking across model generations, so a
sweep run on an earlier model does not carry over. `medium` roughly matches the
previous generation at lower cost.

**Append-only history.** Assistant turns go back exactly as returned, thinking
blocks included, and earlier turns are not edited between requests. On newer
accounts a thinking block is valid only in the exact conversation that produced
it: replaying one after its prefix changed returns a 400. Per-turn reminders
belong in turn-scoped system messages, instruction changes in mid-conversation
system messages, and trimming in server-side compaction or context editing. The
same edits that break the binding also restart the prompt cache.

**Client-side compaction.** If the harness compacts rather than the server, tell
the model what the summary must keep: difficulties and how they were resolved;
options tried or set aside and why; anything decided, ruled out, or established
as a constraint, in the exact words; where things stand; what is still open; and
details that would be hard to reconstruct — names, numbers, exact wording,
links. Keep the user's own words closely; condense the assistant's reasoning
hard. Cache reads are cheap enough now that compacting early to save money may
no longer be the right trade — try later compaction points. The source has the
full instruction to paste.

**Long deliverables at `xhigh` / `max`.** The model can draft a long output in
its thinking and then write it again as the reply, doubling the turn. Run long
deliverables at `high` unless a measured quality gain says otherwise; set
`max_tokens` to cover thinking *and* reply; and tell the model that reasoning
and reply share one budget, so drafting the deliverable twice buys nothing.

**Subagents.** Let the lead agent keep working while subagents run — the
spawning tool returns immediately, results arrive in a later user message, and a
separate tool exists for when the lead wants to wait. Average time to completion
drops at equal quality and cost.

**Vision.** On dense images the model does better with a crop-and-zoom tool, or
a container with image libraries, so it can enlarge a region and look again. Not
currently relevant to this tree.

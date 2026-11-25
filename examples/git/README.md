# git

A subset of the plumbing commands of git, in Fern (#15591). The port reads
and writes the object store of a real repository.

It has the loose objects, the packfiles with their deltas, the refs, the
revisions, and seven commands.

## Commands

```
git hash-object [-w] [-t KIND] [--literally] [--stdin] [FILE...]
git cat-file (-t | -s | -p | -e) NAME
git cat-file (--batch | --batch-check)
git rev-parse [--verify] [-q] [--short[=N]] [--git-dir] [--not] [--end-of-options] ARG...
git verify-pack [-v] FILE.idx...
git ls-tree [-r] [-t] [-d] [-l] [-z] [--name-only] [--object-only]
            [--full-name] [--full-tree] [--abbrev[=N]] TREE [PATH...]
git log [OPTION...] [REVISION...] [-- PATH...]
git index-pack [-v] [-o FILE.idx] [--stdin] [--fix-thin] [--keep[=WHY]]
               [--index-version=N[,OFFSET]] [--[no-]rev-index] [--verify]
               [--strict[=MSG=LEVEL...]] [--fsck-objects[=MSG=LEVEL...]]
               [--threads=N] [FILE.pack]
```

- `hash-object` prints the name of the object for each file, and for the
  standard input with `--stdin`. With `-w` it writes the object to the
  store. A commit, a tree and a tag get the checks of `git fsck`, with the
  messages of git, before the port prints or writes them. `--literally`
  takes an object of any form and any kind.

- `cat-file` prints the kind, the size or the content of an object.
  `--batch` and `--batch-check` take one name for each line of the
  standard input.

- `rev-parse` prints the full name of each revision. A range gives the
  names of its two ends, `A...B` adds the merge bases, and `A^@`, `A^!`
  and `A^-N` give the parents. A name that is not a revision is a path, as
  git reads it.

- `verify-pack` checks a packfile and its index: the two checksums, and the
  name of each object after its deltas. `-v` adds a line for each object
  and the lengths of the delta chains.

- `ls-tree` prints the entries of a tree, for each path when the command
  has paths. In a directory below the top of the worktree, the paths and
  the names are relative to that directory, as git gives them.

- `log` walks the commits as `git log` does. It has the ranges and the
  revisions of `rev-parse`, `--all`, `--branches`, `--tags`, `--remotes`
  and `--not`. It limits the walk with `-n`, `--skip`, `--since`,
  `--until`, `--author`, `--committer`, `--grep` (with `-i`, `-E`, `-F`,
  `--all-match` and `--invert-grep`), `--merges`, `--min-parents`,
  `--max-parents` and `--first-parent`. Paths after the revisions select
  the commits that change them, with the history simplification of git,
  and `--full-history` keeps each side of a merge. The order is that of
  the dates, or that of `--topo-order`, `--date-order`,
  `--author-date-order` and `--reverse`. `--graph` draws the graph of git.
  When more commits come after a root commit, the graph indents the root,
  as git 2.56.1 does.

  `--simplify-merges` and `--simplify-by-decoration` simplify the
  history as git does, and `--ancestry-path` keeps the commits on a path
  through a commit. The sides of `A...B` are `--left-right`,
  `--left-only`, `--right-only`, `--cherry-pick`, `--cherry-mark` and
  `--cherry`, with the patch ids of git. `--boundary` shows the excluded
  parents of the shown commits.

  `-S`, `-G`, `--pickaxe-regex` and `--pickaxe-all` select the commits
  whose diff adds or removes a string or a line that matches. `--follow`
  follows the renames and the copies of one file. `-g` walks the reflogs,
  with the `Reflog:` lines of git and the placeholders `%gd`, `%gD`,
  `%gs`, `%gn`, `%ge`, `%gN` and `%gE`.

  `-L START,END:FILE` and `-L :FUNCNAME:FILE` follow ranges of lines of
  a file back through the history, as `line-log.c` of git does. A place
  of a range is a line number, `+N` or `-N` after the start, `/REGEX/`
  or `^/REGEX/`. Each range moves across the diff of each commit, across
  a rename of its file, and to the parent of a merge that has each
  change. The patch shows the hunks in the ranges, as git 2.56.1 shows
  them, and `--raw`, `--name-only`, `--name-status`, `--summary`, the
  pickaxe, `--parents` and `--graph` also apply.

  The attribute `diff` gives a path its driver, as in `userdiff.c` of
  git. The driver finds the function name of a hunk header, and the
  lines of a function for `-L :FUNCNAME:FILE`. The port has the
  built-in drivers of git 2.56.1 (`diff=cpp`, `diff=python` and the
  others). It also has the drivers of the configuration, with
  `diff.NAME.xfuncname` and `diff.NAME.funcname`. A path with no driver
  has the default rule of git, or the regex of the driver `default`.

  The formats are `--oneline`, `--pretty=` with each named format and
  `format:`, and `--format=`, with the placeholders of git. `%<(N)`,
  `%>(N)`, `%><(N)` and `%>>(N)` set the columns of the next placeholder.
  They also have the forms with `|`, `trunc`, `ltrunc` and `mtrunc`.
  `%w()` wraps the text.

  `--date=` has each date format of git. `--parents`, `--decorate`, `--abbrev-commit`,
  `--name-only` and `--name-status` add to the text. `--decorate=full`,
  `--decorate-refs=`, `--decorate-refs-exclude=`, `--clear-decorations`
  and `log.excludeDecoration` select the refs of the decorations, with
  the default set of git.

  The notes of `refs/notes/` follow the message, as git shows them by
  default and with `--notes`, `--notes=REF`, `--no-notes`, `%N`,
  `core.notesRef`, `notes.displayRef`, `GIT_NOTES_REF` and
  `GIT_NOTES_DISPLAY_REF`.

  `--show-signature`, `log.showSignature` and the placeholders `%GG`,
  `%G?`, `%GS`, `%GK`, `%GF`, `%GP` and `%GT` check the signature of a
  commit. The first line of the signature selects the program, as in
  git: gpg (`gpg.program`), gpgsm for x509 (`gpg.x509.program`), or
  ssh-keygen for ssh (`gpg.ssh.program`). ssh-keygen finds the principal
  in `gpg.ssh.allowedSignersFile` at the date of the commit, and
  `gpg.ssh.revocationFile` holds the revoked keys. `gpg.minTrustLevel`
  sets the least trust of a good signature. `--show-signature` also
  checks the signed tags of a merge (`mergetag`).

  The first check of a signature stops at a value of the configuration
  that git refuses: a `gpg.format` or a `gpg.minTrustLevel` that is not
  known, or one of the `gpg` keys with no value. `log` and `cat-file`
  stop at a key of a driver of the attribute `diff` with no value. The
  message is that of git, with the file and the line of the entry.

  The mailmap of `.mailmap`, `mailmap.blob` and
  `mailmap.file` gives the names of the header lines, of `%aN` and of
  `--author`, as `log.mailmap` and `--use-mailmap` select. The changes of
  `--name-status` find renames as git does. `-C`, `--find-copies`,
  `--find-copies-harder`, `-M<n>` and `diff.renames=copies` find the
  copies and set the least score of a rename or a copy. A tab and a wide character in
  a message take the columns that git gives them.

  The diff of each commit is `-p`, `--raw`, `--stat`, `--numstat`,
  `--shortstat`, `--summary`, `--patch-with-stat` or `--patch-with-raw`, with the bytes of
  git. `--check` shows the errors of white space of the added lines, with
  the rules of `core.whitespace`. It gives the exit status 2 for an
  error, as git does. `--abbrev=N` and `--no-abbrev` set the length of the
  short names.

  The diff of a merge is that of `-m`, `-c`, `--cc`, `--dd`,
  `--remerge-diff`, `--no-diff-merges` and `--diff-merges=` with each
  value of git. `log.diffMerges` sets the diff of `-m`. `-m` shows the
  diff from each parent, with `(from PARENT)` after the name of the
  commit. `-c` and `--cc` show the combined diff of the paths that change
  from each parent, with the hunks and the stat of git.

  `--remerge-diff` merges the two parents of a merge again in memory,
  as merge-ort of git does. It shows the diff from that merge to the
  merge commit. A file with a conflict has the conflict markers of git,
  with the style of `merge.conflictStyle`. The message of each conflict
  is a header line of the diff of its path. A merge of more than two
  parents shows the line of git for it, and no diff.

  The merge finds the renames of files and of directories, with
  `merge.renames`, `merge.renameLimit` and `merge.directoryRenames`. It
  merges two or more merge bases first, and that merge is the base. The
  attributes `merge` and `conflict-marker-size` select the built-in
  driver and the size of the markers of a path.

  `--word-diff` with each of its forms, `--word-diff-regex=`,
  `--color-words` and `diff.wordRegex` show the patch as a diff of
  words. `--color-moved` with each of its forms, `--color-moved-ws=`,
  `diff.colorMoved` and `diff.colorMovedWS` give the moved lines the
  colors of git. The patch
  has the header lines of git, the renames with their similarity, the
  changes of mode, and the line for a binary file. The hunks come from
  `std/linediff`. It has the Myers diff of xdiff with its heuristics and
  the indent heuristic, `--minimal`, `--patience`, `--histogram` and
  `--diff-algorithm=`. The other options are `-U<n>`,
  `--inter-hunk-context=`, `--no-prefix`, `--src-prefix=`,
  `--dst-prefix=`, `--full-index`, `--text`, `--full-diff`,
  `--no-renames`, `-s`, and the widths and the count of `--stat`.

  `--color`, `--color=WHEN`, `--no-color`, `color.ui` and `color.diff`
  give the output the colors of git. The colors are on the header, the
  decorations, the columns of the graph, the patch and the stat, and
  `%C` sets them in a format. `color.diff.SLOT` and `color.decorate.SLOT`
  set the colors of each slot. The patch shows the errors of white space
  of `core.whitespace` and of the attribute `whitespace` of
  `.gitattributes`, `$GIT_DIR/info/attributes` and `core.attributesFile`,
  with the macros. With colors, the matches of `--grep`, `--author` and
  `--committer` in the text of a commit have the colors of
  `color.grep.selected` and `color.grep.matchSelected`, as in git.

- `index-pack` makes the index of a packfile (version 1 or 2) and its
  reverse index, and prints the checksum of the packfile. It reads the
  packfile from a file or from the standard input. For a packfile that is
  not sound, the message and the exit status are those of git.

  `--fix-thin` adds the bases of a thin pack from the store, in the order
  of git. The zlib streams of the bases are those of git at the level of
  `core.loosecompression` or `core.compression`. Thus the packfile, its
  name and its index have the bytes of git.

  `--strict` and `--fsck-objects` check the objects as git does.
  `--verify` writes no file: it compares the index and the reverse index
  of git with those that it makes. `-v` writes the progress of git. Its
  lines of the deltas are those of git with `--threads=1`. Its rate of
  the bytes from the standard input is that of the port.

The port reads and writes repositories with SHA-1 names and with SHA-256
names (`git init --object-format=sha256`). The key
`extensions.objectformat` of the `config` file gives the hash. With no
repository, `hash-object`, `index-pack` and `verify-pack` use SHA-1, as
git does.

A packfile can be larger than 4 GiB. The port finds an object through the
multi-pack index first, as git does. The port maps a packfile of 1 GiB
or less into memory with `map_file_unchecked`, and `index-pack` and
`verify-pack` map the packfile that they read. The port reads a larger
packfile in parts and never as a whole.

A name is a full hash, a short hash of four digits or more, `HEAD`, a ref,
or the output of `git describe`. The port looks for a ref as git does. It
reads loose refs, the file `packed-refs`, the reflogs, and the upstream of
a branch in the configuration.

A name can have operators after it: `^N`, `~N`, `^{KIND}`, `^{}`,
`^{/TEXT}`, `@{N}`, `@{DATE}`, `@{-N}`, `@{upstream}` or `@{u}`, and
`@{push}`, with `push.default`, the push refspecs, `remote.pushDefault`
and `branch.NAME.pushRemote`. `:PATH`
is the entry at a path below a tree, and `:N:PATH` is an entry of the
index. `:/TEXT` is the latest commit whose message matches TEXT.

A short name has the length that git gives it. In a shallow clone, a
commit at the edge has no parent.

The port reads the commit graph of git: the file
`objects/info/commit-graph`, or the chain of files in
`objects/info/commit-graphs/`. It takes the parents, the tree and the date
of a commit from the graph, and reads the commit only for its text.

With generation numbers (the levels of version 1 or the corrected dates
of version 2), `--topo-order`, `--date-order` and `--graph` walk as git
does: they show the first commits before they read the whole history. The
changed-path Bloom filters of version 1 and 2 tell `log -- PATH` which
commits do not change PATH. `commitGraph.changedPathsVersion` and
`commitGraph.readChangedPaths` select the version of the filters, as in
git. `core.commitGraph=false`, a shallow clone, grafts and replace refs
turn the graph off, as in git.

The port finds the repository as git does: `GIT_DIR`, or the `.git` of the
current directory or of a directory above it. A `.git` file of a linked
worktree gives the git directory.
With `GIT_DIR`, the current directory is the top of the worktree, unless
`core.bare` is true.

The standard output, the messages and the exit status are those of git
2.56.1. That is the git of the CI runners. The tests compare the port with
the git of the machine. With git 2.43, these things are different:

- the indent of the roots in `--graph`
- the order of the options in the message for `--graph` with `--reverse`
- the check of the kind in `hash-object --literally`
- the usage of `index-pack`
- `rev-parse --short=N` with N above 40 in a repository with SHA-256
  names. git 2.56.1 gives N digits, and git 2.43 stops at 40.
- the color of the good signature of an expired key of gpg in
  `--show-signature`. git 2.43 gives it the color of a bad signature.
- the diff of `-L`. git 2.43 has its own patch, with no `index` line,
  no function names and a range with no change. It has no other forms
  of the diff and no pickaxe, and its message for `--stat` is not that
  of git 2.56.1.

git 2.43 is also different in these things:

- `--left-only` and `--right-only` with no symmetric range. git 2.56.1
  shows no commit, and git 2.43 shows each commit.
- a conflict marker of `<` or `>` with no space after it. git 2.56.1
  does not give it to `--check`.
- a delta by name whose base is in the packfile two times, and a delta
  whose base is a later delta. git 2.56.1 writes the packfile, and git
  2.43 stops.
- the diff of `--histogram`. git 2.56.1 does a Myers diff again of a
  group of changes that the compaction moved.
- the rule `incomplete-line` of white space. git 2.43 does not have it.

With git 2.43, these things of `--remerge-diff` are different:

- the merge bases of a merge that comes after one of its ancestors in
  the output, as with `--reverse`. git 2.43 drops the parents of each
  commit that it showed.
- the conflicts of a file with lines that come two times, because the
  merge uses the histogram diff.
- `merge.renames` or `diff.renames` set to false. git 2.56.1 finds no
  renames, and git 2.43 finds them.

With git 2.43, these settings, messages and merges are also different:

- `diff.algorithm`. git 2.56.1 merges with that search, and git 2.43
  always merges with the histogram search.
- the message for a `merge.conflictStyle` that git does not know.
- the message for a `conflict-marker-size` that is not an integer.
- a merge where a file is in the way of the new place of a renamed
  directory, or where a file and a directory have one path. git 2.56.1
  corrects some of these merges of git 2.43.

## Files

- `objects.fern`: the hash algorithms, the name of an object, the loose
  object file, and the lines of a tree.
- `pack.fern`: the index of a packfile (version 1 and 2), the header of a
  packed object, and the deltas by offset and by name.
- `store.fern`: the loose objects and the packfiles of one repository.
- `midx.fern`: the multi-pack index.
- `index.fern`: the index of a packfile, made from the packfile.
- `config.fern`: the configuration files.
- `fsck.fern`: the checks of a commit, a tree and a tag.
- `checkblob.fern`: the checks of the `.gitmodules` blobs.
- `checkpack.fern`: the checks of `--strict` and `--fsck-objects`.
- `color.fern`: the colors and the color slots of the output.
- `refs.fern`: the repository, the refs and the reflogs.
- `dircache.fern`: the index of the worktree (versions 2 to 4).
- `rev.fern`: the revisions and their operators.
- `commitgraph.fern`: the commit graph, its generation numbers and its
  Bloom filters.
- `mailmap.fern`: the mailmap.
- `walk.fern`: the graph of the commits and the merge bases.
- `revwalk.fern`: the walk of `log`, with its limits and its orders.
- `treediff.fern`: the changes between two trees, and the renames.
- `graph.fern`: the graph of `--graph`.
- `patch.fern`: the diff of a commit: the patch, `--stat`, `--numstat`,
  `--shortstat` and `--summary`, `--raw`, `--check` and `--word-diff`.
- `combine.fern`: the combined diff of a merge (`-c` and `--cc`).
- `merge.fern`: the merge of two commits for `--remerge-diff`.
- `moved.fern`: the colors of the moved lines (`--color-moved`).
- `ws.fern`: the rules of `core.whitespace` and their errors.
- `attr.fern`: the attributes of `.gitattributes`.
- `userdiff.fern`: the drivers of the attribute `diff`.
- `notes.fern`: the notes of the commits.
- `sig.fern`: the check of a signature with gpg, gpgsm or ssh-keygen,
  and `%G?`.
- `log.fern`: the fields of a commit and the text that `log` prints.
- `linelog.fern`: the ranges of lines of `-L` and their walk through the
  history.
- `date.fern`: the dates that `log` prints and reads.
- `width.fern`: the column width of a character (from `unicode-width.h`).
- `git.fern`: the command line.

## What it does not have yet

- Four parts of the merge of `--remerge-diff`. They are the merge of a
  submodule that the two parents changed, and a merge driver of the
  configuration (`merge.NAME.driver`). They are also
  `merge.renormalize`, and a tree with a mode that git does not write.
  For a merge that needs one of them, the port stops with a message
  that names the part. It also stops at a state of the renames that
  git takes for an internal error.

- The other parts of a driver of the attribute `diff`: `-diff` and
  `diff.NAME.binary` for a binary file, the regex of words of a driver
  for `--word-diff`, and `textconv`.

- The pager. `--color=auto` and `color.ui=auto` give colors only to a
  terminal, as git does without a pager.




## Speed

The times are the CPU time (user and system) of the best of three runs.
The machine is x86-64 with 4 CPUs, and the repository is a clone of
Fern: 292,676 objects, 24,135 commits, and a packfile of 430 MB. The port is built with
`-O`. git is version 2.43.

| Command | git | The port |
|---|---|---|
| `cat-file --batch` (all objects) | 164.5 s | 168.5 s |
| `cat-file --batch-check` (all objects) | 0.68 s | 1.15 s |
| `log` | 0.52 s | 0.65 s |
| `log --oneline` | 0.44 s | 0.49 s |
| `log --oneline -- examples/pigz` | 0.33 s | 0.37 s |
| `log --graph --oneline` | 0.47 s | 0.59 s |
| `log --name-status -200` | 0.026 s | 0.041 s |
| `log --oneline --author=Claude --grep=fix` | 0.51 s | 0.90 s |
| `log --oneline HEAD~500..HEAD` | 0.028 s | 0.043 s |
| `log --oneline --topo-order` | 0.47 s | 0.55 s |
| `log -p -200` | 0.57 s | 0.70 s |
| `log --stat -200` | 0.55 s | 0.67 s |
| `rev-parse HEAD~100 HEAD^{tree} HEAD:README.md` | 0.003 s | 0.005 s |
| `ls-tree -r HEAD` | 0.021 s | 0.029 s |
| `ls-tree -r -l HEAD` | 0.061 s | 0.086 s |
| `index-pack` (430 MB) | 72.0 s | 51.2 s |
| `verify-pack -v` (430 MB) | 72.5 s | 45.7 s |

With a commit graph that has Bloom filters
(`git commit-graph write --reachable --changed-paths`):

| Command | git | The port |
|---|---|---|
| `log --oneline -- examples/pigz` | 0.017 s | 0.038 s |
| `log --graph --oneline` | 0.46 s | 0.70 s |
| `log --oneline --topo-order` | 0.44 s | 0.58 s |
| `log --oneline --author=Claude --grep=fix` | 0.53 s | 0.61 s |

git has a thread for each CPU in `index-pack` and `verify-pack`. The
port has a worker for each CPU in `index-pack`, and one thread in
`verify-pack`. The real time of `index-pack` is 29.3 s for git and
32.3 s for the port. For `verify-pack`, it is 29.6 s and 46.0 s.
At its peak, git uses 248 MB and the port uses 1.7 GB.

In the table, the port uses less CPU time than git only in `index-pack`
and `verify-pack`. The table is older than the change below.

`cat-file --batch-check` and `ls-tree -r -l` need the size of each object,
and the size of a delta is in the first bytes of its stream. The port
reads them with `deflate.inflate_start`, which builds no decode table.
A name of the input is checked and changed to bytes with a table of the
hexadecimal digits. On 2026-11-24, on a clone of Fern with 28,778 objects
in one packfile, x86-64 with 4 CPUs, git 2.43, the best time of seven runs:

| Command | git | The port before | The port |
|---|---|---|---|
| `cat-file --batch-check` (all objects) | 0.037 s | 0.064 s | 0.037 s |
| `ls-tree -r -l HEAD` | 0.046 s | 0.055 s | 0.047 s |

callgrind counts 183.6 M instructions for git in `cat-file --batch-check`.
The port ran 301.5 M, and it runs 188.5 M now. What is left of the gap is
the read of the code lengths of a dynamic block (30.8 M) and the search of
the pack index (18.6 M).

The port inflates each commit one time: the graph of the walk keeps the
commit for the text of `log`. It does not inflate a packed object for its
kind and its size. They come from the header of the object and from the
start of its delta. `cat-file --batch-check` with full names does not
parse a name as a revision.

The port keeps the bases of the deltas in a cache of 96 MB, which each
copy of the store shares. The reverse index of git (`.rev`) gives the
end of each object. Without that file, the port sorts the offsets of the
index. A diff of trees reads the entries of a tree in place, and keeps the
last trees that it read. With a commit graph, the walk reads the parents
and the dates from the graph. A Bloom filter of the graph skips a commit
that does not change the paths.

`index-pack` gives each CPU one part of the packfile, with about the same
count of bytes. A worker of `std/worker` makes the names of its part.

### Comparison with gitoxide

On the zstd v1.5.7 history, Fern walked the log faster than gitoxide and
built the pack index slower. Git built the same index in 2.326 seconds,
Fern in 1.295 seconds and gitoxide in 0.548 seconds.

| Operation | Git wall / CPU (s) | Fern wall / CPU (s) | gitoxide wall / CPU (s) |
|---|---:|---:|---:|
| Full log, abbreviated ID and subject | 0.067 / 0.066 | 0.087 / 0.087 | 0.149 / 0.149 |
| Build a new pack index, four threads | 2.326 / 5.486 | 1.295 / 3.048 | 0.548 / 1.440 |
| Read all objects with `cat-file --batch` | 1.939 / 1.935 | 1.839 / 1.832 | unavailable |

These are medians of three timed runs after one warm-up per tool and
operation, rotating the starting tool each round. All commands ran on the
same four logical CPUs of an AMD EPYC 9V74 Linux host with stdout sent to
`/dev/null`. CPU time includes user and system time. The executables were
Git 2.52.0, the Fern port built with `-O` at `9f606a99da`, and the
gitoxide v0.61.0 `max-pure` x86-64 GNU/Linux release.

The corpus was a single-branch clone of facebook/zstd at
`f8745da6ff1ad1e7bab384bd1f9d742439278e99`, with 11,125 commits and
61,756 objects in one 38,962,986-byte pack
(`pack-bfdef7f520cef6e37689e060e5947dab6e6a109f`). To repeat each row:

- Run `git log --oneline --abbrev=8`, the Fern port with the same arguments,
  and `gix log`. Git and Fern produced identical bytes. All three emitted
  the same commit IDs. Some subjects and ordering differed in the gix output.
- Hard-link the pack into a fresh temporary directory for each trial.
  Run Git and Fern with `index-pack --threads=4 "$tmp/input.pack"`.
  Run gitoxide with `gix --threads 4 free pack index create -p "$tmp/input.pack" "$tmp"`.
  Compare the generated index with the original after timing. All three
  produced byte-identical indexes in every trial.
- Write the input list with `git cat-file --batch-all-objects --batch-check='%(objectname)'`.
  Feed that file to Git and Fern in separate `cat-file --batch` runs.
  Both output streams had SHA-256
  `b22301de6a18c7258f00f3fe6e228a488dbcc15e8546ffadcf5efea6a2aacd12`.
  The gitoxide CLI accepts a single revision per `gix cat` invocation and
  has no equivalent batch command.

## Tests

- `git_test.fern` holds the unit cases. It runs on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleGitUnit`).

- `TestSelfHostExampleGitAgainstGit` makes a repository with git and runs
  each command with git and with the port, at the top of the worktree and
  in two directories below it. The port then writes an object, and git
  reads it and checks the store with `fsck --strict`.

- `TestSelfHostExampleGitPacked` packs a repository with chains of deltas,
  by offset and then by name. Each object and each line of `verify-pack -v`
  are those of git. The last commit of the repository is a merge, and the
  operators give each parent. The port refuses a packfile with one changed
  byte. The index and the reverse index of `index-pack` have the bytes of
  git.

  The test also has a shallow clone, and a store of 16,400 objects
  where a short name has 8 digits.

- Each of these tests runs on x86-64 and on arm64, in a repository with
  SHA-1 names and in one with SHA-256 names.

- `TestSelfHostExampleGitHashObject` gives `hash-object` sound and unsound
  commits, trees and tags. `TestSelfHostExampleGitLargePack` reads a
  sparse packfile of more than 4 GiB, made at test time.
  `gitIndexPackCases` runs `index-pack` on each form of its input, and on
  packfiles with a changed byte at each place of their first objects.
  It also runs `-v`, and `--verify` with indexes that are not those of
  git.

- `TestSelfHostExampleGitRevisions` runs `rev-parse` on each form of a
  revision, with the reflogs, an upstream and an index with conflicts.

- `TestSelfHostExampleGitLog` runs 121 forms of `log`, and
  `TestSelfHostExampleGitLogRandom` runs them on a random history with
  merges and renames.

- `TestSelfHostExampleGitDiff` runs 78 forms of the diff of `log` on a
  repository with each kind of change, and the patches of random edits
  of code with each search of xdiff.

- `TestSelfHostExampleGitCommitGraph` runs random forms of `log` in
  random histories with a commit graph: one file with Bloom filters, a
  chain of two files, and one file with the generation numbers of
  version 1. Then it changes a date in the graph, and the port must use
  that date as git does, and not when `core.commitGraph` is false.

- `TestSelfHostExampleGitOptions` runs the mailmap, the decorations, the
  sides of a range, the boundary, the simplifications, the reflog walk,
  the pickaxe and `--follow`. `TestSelfHostExampleGitColor` runs the
  forms of `log` with colors.

- `TestSelfHostExampleGitForms` runs the placeholders that change the
  columns of a format, the wrap of `%w()`, `--raw`, and `--check` with
  each rule of `core.whitespace`. It runs the diffs of merges, also in a
  random history of merges with conflicts. It runs the diffs of words
  and the moved lines, also of random edits. It runs the copies, also in
  a random history, the notes, and the signatures of two keys of gpg.

- `TestSelfHostExampleGitRemerge` runs 100 forms of `log` for the diff
  of a merge, most of them with `--remerge-diff`. The repository has
  each kind of merge: clean merges, each kind of conflict, renamed files
  and directories, two merge bases, and a merge of three parents. The
  test runs them with each style of conflict and with the other
  settings of a merge. It runs them with the attributes of a merge. It
  also runs random histories of merges, and the merges that the port
  refuses.

- `TestSelfHostExampleGitSignatures` runs the signatures of ssh-keygen,
  gpgsm and gpg: the keys of the allowed signers, an unknown key, a
  revoked key, a key that is not valid at the date of its commit, an
  expired key of gpg, a bad signature, the signed tags of merges, and a
  signature of no known format.

- `TestSelfHostExampleGitLineLog` runs each form of `-L` and of its
  output, with merges, a rename, a binary file and the errors of git,
  at the top of the worktree, below it, and with a commit graph.

- `TestSelfHostExampleGitFixThin` runs `index-pack --fix-thin` on a thin
  pack of `pack-objects` at each level of zlib, and on deltas whose bases
  are deltas.

- `TestSelfHostExampleGitPush` reads `@{push}` with each value of
  `push.default` and the other settings of a push.

- `TestSelfHostExampleGitMultiPackIndex` changes the multi-pack index, and
  the port must read the objects through it as git does.

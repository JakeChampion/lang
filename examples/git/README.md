# git

A subset of the plumbing commands of git, in Fern (#15591). The port reads
and writes the object store of a real repository.

This is slices 1 to 4: loose objects, packfiles with their deltas, the
refs, the operators of a revision, and seven commands.

## Commands

```
git hash-object [-w] [-t KIND] [--stdin] [FILE...]
git cat-file (-t | -s | -p | -e) NAME
git cat-file (--batch | --batch-check)
git rev-parse [--verify] [--short[=N]] [--git-dir] NAME...
git verify-pack [-v] FILE.idx...
git ls-tree [-r] [-t] [-d] [-l] [--name-only] [--full-tree] TREE
git log [-n N] [--oneline] [--format=FORMAT] [NAME]
git index-pack [-o FILE.idx] FILE.pack
```

- `hash-object` prints the name of the object for each file, and for the
  standard input with `--stdin`. With `-w` it writes the object to the store.

- `cat-file` prints the kind, the size or the content of an object. `-p`
  prints a tree as git does: the mode, the kind, the name and the path, and
  a path in quotes when git puts it in quotes. `--batch` and `--batch-check`
  take one name for each line of the standard input.

- `rev-parse` prints the full name of each object.

- `verify-pack` checks a packfile and its index: the two checksums, and the
  name of each object after its deltas. `-v` adds a line for each object
  and the lengths of the delta chains.

- `ls-tree` prints the entries of a tree. `-r` goes into each tree below
  it, `-t` shows the trees also, `-d` shows the trees only, and `-l` adds
  the size of each blob. In a directory below the top of the worktree, it
  lists the tree of that directory, as git does.

- `log` prints the commits that a name reaches, the latest committer date
  first. `-n N`, `-N` and `--max-count=N` set the count of commits.
  `--oneline` prints the short name and the subject.

  `--format=FORMAT` prints a line for each commit. `--pretty=format:FORMAT`
  puts a line end between two commits and not after each one. The
  placeholders are `%H`, `%h`, `%T`, `%t`, `%P`, `%p`, `%s`, `%b`, `%B`,
  `%n` and `%%`. For the author they are `%an`, `%ae`, `%ad` and `%at`,
  and `%cn`, `%ce`, `%cd` and `%ct` for the committer.

- `index-pack` makes the index of a packfile and its reverse index, and
  prints the checksum of the packfile. The two files have the bytes that
  git writes.

A name is a full hash, a short hash of four digits or more, `HEAD`, or a
ref. The port looks for a ref as git does: the name, then the name below
`refs/`, `refs/tags/`, `refs/heads/` and `refs/remotes/`. It reads loose
refs and the file `packed-refs`.

A name can have operators after it. `^N` is parent `N` of a commit, and
`~N` is the first parent `N` times. `^{KIND}` follows tags and commits to
an object of that kind, and `^{}` follows tags only. `:PATH` is the entry
at a path below the tree.

A short name has the length that git gives it: 7 digits, or more for a
store with many packed objects. In a shallow clone, a commit at the edge
has no parent.

The port finds the repository as git does: `GIT_DIR`, or the `.git` of the
current directory or of a directory above it. A `.git` file of a linked
worktree gives the git directory.

The standard output, the messages and the exit status are those of git
2.43.

## Files

- `objects.fern`: the name of an object, the loose object file, and the
  lines of a tree.
- `pack.fern`: the index of a packfile (version 2), the header of a packed
  object, and the deltas by offset and by name.
- `store.fern`: the loose objects and the packfiles of one repository.
- `refs.fern`: the repository of the current directory, and the names.
- `rev.fern`: the operators of a revision.
- `log.fern`: the fields of a commit and the text that `log` prints.
- `index.fern`: the index of a packfile, made from the packfile.
- `git.fern`: the command line.

## What it does not have yet

- A packfile larger than 2 GiB, and the index of version 1.

- The multi-pack index and the commit graph. The port reads each index of
  `objects/pack`.

- `index-pack` on more than one CPU.

- The other forms of a revision: `@{N}`, `^{/TEXT}`, `:/TEXT` and the
  ranges. git prints one more line when `^` or `~` follows an object that
  is not a commit, and the port does not print it.

- `ls-tree` with a path after the tree.

- `hash-object -w` for a commit, a tree or a tag. git checks the form of
  such an object before it writes one, and the port does not have that
  check. The port prints the name of such an object without `-w`.

- `log` with a range, a path, a graph, a diff, or a date format. The
  default text gives a tab the width of 8 columns, and a wide character
  has one column.

- `index-pack` from the standard input, and the completion of a thin
  pack. For a packfile that is not sound, the exit status is that of git
  and the message is not.

- Repositories with SHA-256 names.

## Speed

The times are for one run on x86-64, on the repository of Fern: 34,339
objects, 978 commits in a shallow clone, and a packfile of 275 MB. git 2.43
has four threads for `index-pack`, and the port has one.

| Command | git | The port |
|---|---|---|
| `cat-file --batch` (34,339 objects, 1.2 GB) | 5.9 s | 3.8 s |
| `log` (978 commits) | 0.02 s | 0.04 s |
| `ls-tree -r HEAD` | 0.02 s | 0.05 s |
| `ls-tree -r -l HEAD` | 0.05 s | 0.26 s |
| `cat-file --batch-check` (34,339 objects) | 0.07 s | 0.45 s |
| `index-pack` (3.3 MB) | 0.14 s | 0.16 s |
| `index-pack` (275 MB) | 3.0 s | 7.9 s |

The port does not inflate a packed object for its kind and its size. They
come from the header of the object and from the start of its delta.

The port keeps the bases of the deltas in a cache of 96 MB. For a packfile
above 1 MiB, it reads the bytes of one object from the file. It reads the
whole file after such reads for half of the objects.

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

  The packfile is above 1 MiB, so the port reads it in parts and then
  whole. The test also has a shallow clone, and a store of 16,400 objects
  where a short name has 8 digits.

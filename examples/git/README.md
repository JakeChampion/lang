# git

A subset of the plumbing commands of git, in Fern (#15591). The port reads
and writes the object store of a real repository.

This is slices 1 to 3: loose objects, packfiles with their deltas, the
refs, the operators of a revision, and five commands.

## Commands

```
git hash-object [-w] [-t KIND] [--stdin] [FILE...]
git cat-file (-t | -s | -p | -e) NAME
git cat-file (--batch | --batch-check)
git rev-parse [--verify] [--short[=N]] [--git-dir] NAME...
git verify-pack [-v] FILE.idx...
git ls-tree [-r] [-t] [-d] [-l] [--name-only] [--full-tree] TREE
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

A name is a full hash, a short hash of four digits or more, `HEAD`, or a
ref. The port looks for a ref as git does: the name, then the name below
`refs/`, `refs/tags/`, `refs/heads/` and `refs/remotes/`. It reads loose
refs and the file `packed-refs`.

A name can have operators after it. `^N` is parent `N` of a commit, and
`~N` is the first parent `N` times. `^{KIND}` follows tags and commits to
an object of that kind, and `^{}` follows tags only. `:PATH` is the entry
at a path below the tree.

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
- `git.fern`: the command line.

## What it does not have yet

- A packfile larger than 2 GiB, and the index of version 1.
- The multi-pack index and the commit graph. The port reads each index of
  `objects/pack`, and it reads a whole packfile when it needs one object
  of it.
- The other forms of a revision: `@{N}`, `^{/TEXT}`, `:/TEXT` and the
  ranges. git prints one more line when `^` or `~` follows an object that
  is not a commit, and the port does not print it.
- `ls-tree` with a path after the tree.
- `hash-object -w` for a commit, a tree or a tag. git checks the form of
  such an object before it writes one, and the port does not have that
  check. The port prints the name of such an object without `-w`.
- `log` and `index-pack`.
- Repositories with SHA-256 names.

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
  byte.

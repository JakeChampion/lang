# git

A subset of the plumbing commands of git, in Fern (#15591). The port reads
and writes the object store of a real repository.

This is slice 1: loose objects, the refs, and three commands.

## Commands

```
git hash-object [-w] [-t KIND] [--stdin] [FILE...]
git cat-file (-t | -s | -p | -e) NAME
git cat-file (--batch | --batch-check)
git rev-parse [--verify] [--short[=N]] [--git-dir] NAME...
```

- `hash-object` prints the name of the object for each file, and for the
  standard input with `--stdin`. With `-w` it writes the object to the store.
- `cat-file` prints the kind, the size or the content of an object. `-p`
  prints a tree as git does: the mode, the kind, the name and the path, and
  a path in quotes when git puts it in quotes. `--batch` and `--batch-check`
  take one name for each line of the standard input.
- `rev-parse` prints the full name of each object.

A name is a full hash, a short hash of four digits or more, `HEAD`, or a
ref. The port looks for a ref as git does: the name, then the name below
`refs/`, `refs/tags/`, `refs/heads/` and `refs/remotes/`. It reads loose
refs and the file `packed-refs`.

The port finds the repository as git does: `GIT_DIR`, or the `.git` of the
current directory or of a directory above it. A `.git` file of a linked
worktree gives the git directory.

The standard output, the messages and the exit status are those of git
2.43.

## Files

- `objects.fern`: the name of an object, the loose object file, and the
  lines of a tree.
- `refs.fern`: the repository of the current directory, and the names.
- `git.fern`: the command line.

## What it does not have yet

- Packfiles. An object that is only in a packfile is not found, and a short
  hash is compared with the loose objects only. Slice 2 adds them.
- The operators of a revision: `^`, `~`, `^{tree}` and `:path`.
- `hash-object -w` for a commit, a tree or a tag. git checks the form of
  such an object before it writes one, and the port does not have that
  check. The port prints the name of such an object without `-w`.
- `ls-tree`, `log`, `verify-pack` and `index-pack`.
- Repositories with SHA-256 names.

## Tests

- `git_test.fern` holds the unit cases. It runs on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleGitUnit`).
- `TestSelfHostExampleGitAgainstGit` makes a repository with git and runs
  each command with git and with the port, at the top of the worktree and
  in a directory below it. The port then writes an object, and git reads it
  and checks the store with `fsck --strict`.

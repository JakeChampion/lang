# POSIX path text

This module processes POSIX paths without filesystem access.

## Model

Path helpers operate on POSIX path strings. They separate filenames, extensions, parent paths, and normalized path components.

## Contracts

These operations do not consult the filesystem. Text cleanup cannot establish whether a path exists or whether a symbolic link changes its destination.

## Executable example

```fern test name=std-path-example
import "std/path";

function main(): i32 {
  if (path.clean("a/./b/../c") != "a/c") { return 1; }
  if (path.extension("main.fern") != "fern") { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function join(parts: string[]): string>>

<<3 function seg_pop(xs: string[]): string[]>>

<<4 pub function clean(p: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module processes POSIX paths without filesystem access.
```

### `2 pub function join(parts: string[]): string`

```fern
<<2 pub function join(parts: string[]): string>>=
pub function join(parts: string[]): string {
  let n: i32 = parts.len();
  if (n == 0) {
    return "";
  }
  let out: string = "";
  let i: i32 = 0;
  while (i < n) {
    let p: string = parts[i];
    if (p.len() == 0) {
      i = i + 1;
    } else {
      if (out.len() == 0) {
        out = p;
      } else {
        while (out.len() > 1 && out[out.len() - 1] == 47) {
          out = slice_unchecked(out, 0, out.len() - 1) + "";
        }
        let start: i32 = 0;
        while (start < p.len() && p[start] == 47) {
          start = start + 1;
        }
        let rest: str = slice_unchecked(p, start, p.len());
        if (out[out.len() - 1] == 47) {
          out = out + rest;
        } else {
          out = out + "/" + rest;
        }
      }
      i = i + 1;
    }
  }
  return out;
}

pub function parent(p: string): string {
  let n: i32 = p.len();
  if (n == 0) {
    return "";
  }
  let i: i32 = n - 1;
  if (n == 1 && p[0] == 47) {
    return "/";
  }
  while (i > 0 && p[i] == 47) {
    i = i - 1;
  }
  while (i >= 0 && p[i] != 47) {
    i = i - 1;
  }
  if (i < 0) {
    return "";
  }
  if (i == 0) {
    return "/";
  }
  return slice_unchecked(p, 0, i) + "";
}

pub function file_name(p: string): string {
  let n: i32 = p.len();
  if (n == 0) {
    return "";
  }
  let end: i32 = n;
  while (end > 0 && p[end - 1] == 47) {
    end = end - 1;
  }
  if (end == 0) {
    return "";
  }
  let i: i32 = end - 1;
  while (i >= 0 && p[i] != 47) {
    i = i - 1;
  }
  return slice_unchecked(p, i + 1, end) + "";
}

pub function extension(p: string): string {
  let name: string = file_name(p);
  let n: i32 = name.len();
  if (n == 0) {
    return "";
  }
  let i: i32 = n - 1;
  while (i > 0 && name[i] != 46) {
    i = i - 1;
  }
  if (i <= 0) {
    return "";
  }
  return slice_unchecked(name, i + 1, n) + "";
}
```

### `3 function seg_pop(xs: string[]): string[]`

```fern
<<3 function seg_pop(xs: string[]): string[]>>=
function seg_pop(xs: string[]): string[] {
  let out: string[] = [];
  let last: i32 = xs.len() - 1;
  let i: i32 = 0;
  while (i < last) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}
```

### `4 pub function clean(p: string): string`

```fern
<<4 pub function clean(p: string): string>>=
pub function clean(p: string): string {
  let n: i32 = p.len();
  if (n == 0) {
    return ".";
  }
  let rooted: boolean = p[0] == 47;
  let segs: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    while (i < n && p[i] == 47) {
      i = i + 1;
    }
    if (i >= n) {
      i = n;
    } else {
      let start: i32 = i;
      while (i < n && p[i] != 47) {
        i = i + 1;
      }
      let seg: str = slice_unchecked(p, start, i);
      if (seg == ".") {} else if (seg == "..") {
        let slen: i32 = segs.len();
        if (slen > 0 && segs[slen - 1] != "..") {
          segs = seg_pop(segs);
        } else if (!rooted) {
          segs = segs.append("..");
        }
      } else {
        segs = segs.append(seg + "");
      }
    }
  }
  let joined: string = "";
  let k: i32 = 0;
  while (k < segs.len()) {
    if (k > 0) {
      joined = joined + "/";
    }
    joined = joined + segs[k];
    k = k + 1;
  }
  if (rooted) {
    return "/" + joined;
  }
  if (joined.len() == 0) {
    return ".";
  }
  return joined;
}

pub function is_absolute(p: string): boolean {
  return p.len() > 0 && p[0] == 47;
}

pub function stem(p: string): string {
  let name: string = file_name(p);
  let n: i32 = name.len();
  if (n == 0) {
    return "";
  }
  let i: i32 = n - 1;
  while (i > 0 && name[i] != 46) {
    i = i - 1;
  }
  if (i <= 0) {
    return name;
  }
  return slice_unchecked(name, 0, i) + "";
}

pub function with_extension(p: string, ext: string): string {
  let n: i32 = p.len();
  let end: i32 = n;
  while (end > 0 && p[end - 1] == 47) {
    end = end - 1;
  }
  if (end == 0) {
    if (ext.len() == 0) {
      return p;
    }
    return p + "." + ext;
  }
  let fs: i32 = end - 1;
  while (fs >= 0 && p[fs] != 47) {
    fs = fs - 1;
  }
  fs = fs + 1;
  let dot: i32 = end;
  let j: i32 = end - 1;
  while (j > fs && p[j] != 46) {
    j = j - 1;
  }
  if (j > fs && p[j] == 46) {
    dot = j;
  }
  let base: string = slice_unchecked(p, 0, dot) + "";
  if (ext.len() == 0) {
    return base;
  }
  return base + "." + ext;
}
```

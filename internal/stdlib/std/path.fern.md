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
  let n = parts.len();
  if (n == 0) {
    return "";
  }
  let out = "";
  let i = 0;
  while (i < n) {
    let p = parts[i];
    if (p.len() == 0) {
      i += 1;
    } else {
      if (out.len() == 0) {
        out = p;
      } else {
        while (out.len() > 1 && out[out.len() - 1] == 47) {
          out = slice_unchecked(out, 0, out.len() - 1) + "";
        }
        let start = 0;
        while (start < p.len() && p[start] == 47) {
          start += 1;
        }
        let rest = slice_unchecked(p, start, p.len());
        if (out[out.len() - 1] == 47) {
          out += rest;
        } else {
          out = out + "/" + rest;
        }
      }
      i += 1;
    }
  }
  return out;
}

pub function parent(p: string): string {
  let n = p.len();
  if (n == 0) {
    return "";
  }
  let i = n - 1;
  if (n == 1 && p[0] == 47) {
    return "/";
  }
  while (i > 0 && p[i] == 47) {
    i -= 1;
  }
  while (i >= 0 && p[i] != 47) {
    i -= 1;
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
  let n = p.len();
  if (n == 0) {
    return "";
  }
  let end = n;
  while (end > 0 && p[end - 1] == 47) {
    end -= 1;
  }
  if (end == 0) {
    return "";
  }
  let i = end - 1;
  while (i >= 0 && p[i] != 47) {
    i -= 1;
  }
  return slice_unchecked(p, i + 1, end) + "";
}

pub function extension(p: string): string {
  let name = file_name(p);
  let n = name.len();
  if (n == 0) {
    return "";
  }
  let i = n - 1;
  while (i > 0 && name[i] != 46) {
    i -= 1;
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
  let last = xs.len() - 1;
  let i = 0;
  while (i < last) {
    out = out.append(xs[i]);
    i += 1;
  }
  return out;
}
```

### `4 pub function clean(p: string): string`

```fern
<<4 pub function clean(p: string): string>>=
pub function clean(p: string): string {
  let n = p.len();
  if (n == 0) {
    return ".";
  }
  let rooted = p[0] == 47;
  let segs: string[] = [];
  let i = 0;
  while (i < n) {
    while (i < n && p[i] == 47) {
      i += 1;
    }
    if (i >= n) {
      i = n;
    } else {
      let start = i;
      while (i < n && p[i] != 47) {
        i += 1;
      }
      let seg = slice_unchecked(p, start, i);
      if (seg == ".") {} else if (seg == "..") {
        let slen = segs.len();
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
  let joined = "";
  let k = 0;
  while (k < segs.len()) {
    if (k > 0) {
      joined += "/";
    }
    joined += segs[k];
    k += 1;
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
  let name = file_name(p);
  let n = name.len();
  if (n == 0) {
    return "";
  }
  let i = n - 1;
  while (i > 0 && name[i] != 46) {
    i -= 1;
  }
  if (i <= 0) {
    return name;
  }
  return slice_unchecked(name, 0, i) + "";
}

pub function with_extension(p: string, ext: string): string {
  let n = p.len();
  let end = n;
  while (end > 0 && p[end - 1] == 47) {
    end -= 1;
  }
  if (end == 0) {
    if (ext.len() == 0) {
      return p;
    }
    return p + "." + ext;
  }
  let fs = end - 1;
  while (fs >= 0 && p[fs] != 47) {
    fs -= 1;
  }
  fs += 1;
  let dot = end;
  let j = end - 1;
  while (j > fs && p[j] != 46) {
    j -= 1;
  }
  if (j > fs && p[j] == 46) {
    dot = j;
  }
  let base = slice_unchecked(p, 0, dot) + "";
  if (ext.len() == 0) {
    return base;
  }
  return base + "." + ext;
}
```

# File times

This module reads and sets the access and modification times of files as `time.Timestamp` values.

## Model

`stat`, `lstat` and the `stat` of a reader or a writer give a `FileStat` with each time in seconds and nanoseconds. `accessed`, `modified`, `changed` and `created` give these times as timestamps. A file system that does not record the time a file was made gives a zero birth time, and `created` gives None for it.

`set` sets the access time and the modification time of a path, and `set_modified` sets only the modification time. `touch` sets both to the time of the clock. Each one follows a symbolic link. The status-change time is the time of the last change of the metadata, so no call can set it.

## Contracts

Each function that sets a time gives the error of the host, for example when the file does not exist. The precision of a stored time is the precision of the file system.

## Executable example

```fern test name=std-filetime-example
import "std/filetime";
import "std/io";
import "std/time";

function main(): i32 {
  let Ok(tmp) = io.temp_file("filetime") else {
    return 1;
  };
  let t = time.timestamp_from_unix(1700000000);
  let set = filetime.set(tmp.path, t, t);
  let st = stat(tmp.path);
  let removed = remove_file(tmp.path);
  if (set.is_err()) {
    return 2;
  }
  let Ok(s) = st else {
    return 3;
  };
  if (s.modified().sec != 1700000000) {
    return 4;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<reading>>

<<setting>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and sets the times of files as timestamps.
import "std/result";
import "std/time";
```

### `reading`

```fern
<<reading>>=
pub function (s: FileStat) accessed(): time.Timestamp {
  return time.Timestamp { sec: s.atime, nsec: s.atime_nsec as i32 };
}

pub function (s: FileStat) modified(): time.Timestamp {
  return time.Timestamp { sec: s.mtime, nsec: s.mtime_nsec as i32 };
}

// The time of the last change of the data or the metadata of the file.
pub function (s: FileStat) changed(): time.Timestamp {
  return time.Timestamp { sec: s.ctime, nsec: s.ctime_nsec as i32 };
}

// The time the file was made, or None where the file system does not
// record it.
pub function (s: FileStat) created(): Option[time.Timestamp] {
  if (s.btime == 0 && s.btime_nsec == 0) {
    return None;
  }
  return Some(time.Timestamp { sec: s.btime, nsec: s.btime_nsec as i32 });
}
```

### `setting`

The flags of the `set_file_times` builtin: 2 keeps the access time, and 8 and 16 set the access time and the modification time to the time of the clock.

```fern
<<setting>>=
const KEEP_ACCESSED: i32 = 2;

const NOW_ACCESSED: i32 = 8;

const NOW_MODIFIED: i32 = 16;

// Sets the access time and the modification time of `path`.
pub function set(path: string, accessed: time.Timestamp, modified: time.Timestamp): Result[void, IoError] {
  return set_file_times(path, accessed.sec, accessed.nsec as i64, modified.sec, modified.nsec as i64, 0);
}

// Sets the modification time of `path`, and keeps its access time.
pub function set_modified(path: string, modified: time.Timestamp): Result[void, IoError] {
  return set_file_times(path, 0, 0, modified.sec, modified.nsec as i64, KEEP_ACCESSED);
}

// Sets the access time and the modification time of `path` to the time of
// the clock. The file must exist.
pub function touch(path: string): Result[void, IoError] {
  return set_file_times(path, 0, 0, 0, 0, NOW_ACCESSED | NOW_MODIFIED);
}
```

# Durable checkpoints for the database and archive

Set `FERN_LABS_DATA` to a directory on a persistent volume. Without this setting, the two applications use temporary memory.
A checkpoint contains a SHA-256 checksum and a JSON document. Recovery rejects a damaged checkpoint.

The writer synchronizes a temporary file, atomically replaces the checkpoint, and synchronizes the directory.
The caller publishes the new revision only after this function succeeds. A failure blocks further writes until recovery.
Incomplete temporary files never replace a valid checkpoint during recovery.

```fern
<<*>>=
<<Checkpoint operations>>
```

## Checkpoint operations

The caller supplies a validated project name. Read errors remain errors unless the checkpoint does not exist.
Each checkpoint has a 16 MiB limit. Both success and failure paths close their descriptors.

```fern
<<Checkpoint operations>>=
import "std/result";
import "std/option";
import "std/crypto";
import "std/hex";
import "std/string";
import "std/array";

pub function load(directory: string, name: string): Result[Option[string], string] {
  if (directory == "") {
    return Ok(None);
  }
  let path = directory + "/" + name + ".json";
  match (stat(path)) {
    Ok(info) => {
      if (info.size > 16777216) {
        return Err("Saved project exceeds 16 MiB.");
      }
    },
    Err(error) => {
      if let NotFound(_) = error {
        return Ok(None);
      }
      return Err(error.message());
    }
  }
  let record = read_file(path).map_err((e: IoError): string => e.message())?;
  let fields = record.splitn("\n", 2);
  if (fields.len() != 2 || fields[0] != hex.encode(crypto.sha256_new().update(fields[1].as_bytes()).finish())) {
    return Err("Saved project checksum does not match.");
  }
  return Ok(Some(fields[1]));
}

pub function save(directory: string, name: string, payload: string): Result[void, string] {
  if (directory == "") {
    return Ok(());
  }
  if (payload.len() > 16777000) {
    return Err("Saved project exceeds 16 MiB.");
  }
  create_dir_all(directory).map_err((e: IoError): string => e.message())?;
  let path = directory + "/" + name + ".json";
  let record = hex.encode(crypto.sha256_new().update(payload.as_bytes()).finish()) + "\n" + payload;
  let writer = open_writer(path + ".next").map_err((e: IoError): string => e.message())?;
  if let Err(error) = writer.write(record) {
    let closed = writer.close();
    return Err(error.message());
  }
  let synced = writer.fsync();
  let closed = writer.close();
  synced.map_err((e: IoError): string => e.message())?;
  closed.map_err((e: IoError): string => e.message())?;
  rename(path + ".next", path).map_err((e: IoError): string => e.message())?;
  let reader = open_reader(directory).map_err((e: IoError): string => e.message())?;
  let directory_synced = reader.fsync();
  let directory_closed = reader.close();
  directory_synced.map_err((e: IoError): string => e.message())?;
  return directory_closed.map_err((e: IoError): string => e.message());
}
```

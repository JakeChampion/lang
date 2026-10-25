# Blockyard: journal, snapshot, and crash recovery

The optional data directory belongs to a persistent volume. Each world has one owning server process.
Without that directory, the application explicitly reports temporary storage.

A batch enters an append-only journal and completes `fsync` before the world confirms its blocks.
Snapshots use a temporary file, file synchronization, atomic rename, and directory synchronization.
Recovery checks a SHA-256 checksum and a contiguous revision sequence before it accepts a record.

```fern
<<*>>=
<<Records>>
<<File operations>>
<<Recovery>>
<<Commit>>
```

## Records

Every record occupies one line. Its checksum precedes its compact JSON payload.
A snapshot contains all procedural-terrain differences. A journal batch contains only new block changes.
The file limit prevents an invalid data volume from exhausting server memory.

```fern
<<Records>>=
import "core/map";
import "std/array";
import "std/string";
import "std/json";
import "std/crypto";
import "std/hex";
import "std/result";
import "std/option";
import "./voxel";
import "./blockyard_world" as model;

@derive(json.Json, json.FromJson)
struct SavedBlock { at: i32, material: i32 }

@derive(json.Json, json.FromJson)
struct Snapshot { version: i32, revision: i64, blocks: SavedBlock[] }

@derive(json.Json, json.FromJson)
struct Batch { version: i32, revision: i64, changes: model.Change[] }

pub struct Recovered { blocks: Map[i32, i32], revision: i64, journal_bytes: i64 }

pub struct Store { directory: string, name: string, bytes: Cell[i64] }

function envelope(payload: string): string {
  return hex.encode(crypto.sha256(payload.as_bytes())) + " " + payload + "\n";
}

function payload(record: string): Result[string, string] {
  let fields = record.splitn(" ", 2);
  if (fields.len() != 2 || fields[0].len() != 64 || fields[0] != hex.encode(crypto.sha256(fields[1].as_bytes()))) {
    return Err("World record checksum does not match.");
  }
  return Ok(fields[1]);
}

function valid(at: i32, value: i32): boolean {
  return at >= 262144 && at < 47 * 262144 && value >= 0 && value <= 10;
}

pub function store(directory: string, name: string, bytes: i64): Store {
  return Store { directory: directory, name: name, bytes: cell_new(bytes) };
}
```

## File operations

Always close a descriptor after a failed write or synchronization.
An absent file is a new world. Every other read failure remains an error.

```fern
<<File operations>>=
function text(path: string): Result[Option[string], string] {
  match (stat(path)) {
    Ok(info) => {
      if (info.size > 33554432) {
        return Err("World file exceeds its recovery limit.");
      }
      return read_file(path).map((value: string): Option[string] => Some(value)).map_err((e: IoError): string => e.message());
    },
    Err(error) => {
      if let NotFound(_) = error {
        return Ok(None);
      }
      return Err(error.message());
    }
  }
  return Err("Cannot inspect world file.");
}

function sync_close(writer: Writer, result: Result[void, IoError]): Result[void, string] {
  if let Err(error) = result {
    let closed = writer.close();
    return Err(error.message());
  }
  let synced = writer.fsync();
  let closed = writer.close();
  synced.map_err((e: IoError): string => e.message())?;
  return closed.map_err((e: IoError): string => e.message());
}

function sync_directory(directory: string): Result[void, string] {
  let reader = open_reader(directory).map_err((e: IoError): string => e.message())?;
  let synced = reader.fsync();
  let closed = reader.close();
  synced.map_err((e: IoError): string => e.message())?;
  return closed.map_err((e: IoError): string => e.message());
}

function atomic(path: string, directory: string, value: string): Result[void, string] {
  let writer = open_writer(path + ".tmp").map_err((e: IoError): string => e.message())?;
  sync_close(writer, writer.write(value))?;
  rename(path + ".tmp", path).map_err((e: IoError): string => e.message())?;
  return sync_directory(directory);
}

function trim_journal(path: string, length: i64): Result[void, string] {
  let writer = open_appender(path).map_err((e: IoError): string => e.message())?;
  return sync_close(writer, writer.truncate(length));
}
```

## Recovery

A final record without a newline was not a complete journal batch. Recovery truncates that incomplete tail before the next append.
A complete record with a bad checksum stops recovery. The server never silently replaces damaged world data.
Journal records already included in a snapshot are safe to skip after a crash during compaction.

```fern
<<Recovery>>=
pub function recover(directory: string, name: string): Result[Recovered, string] {
  let blocks: Map[i32, i32] = map_new(64);
  let revision: i64 = 0;
  if (directory == "") {
    return Ok(Recovered { blocks: blocks, revision: revision, journal_bytes: 0 });
  }
  create_dir_all(directory).map_err((e: IoError): string => e.message())?;
  let base = directory + "/" + name;
  if let Some(source) = text(base + ".snapshot")? {
    if (!source.ends_with("\n")) {
      return Err("World snapshot is incomplete.");
    }
    let body = payload(source.take(source.len() - 1))?;
    let snapshot = json.decode[Snapshot](body).map_err((error: json.JsonError): string => error.message())?;
    if (snapshot.version != 1 || snapshot.revision < 0 || snapshot.blocks.len() > model.MAX_EDITS) {
      return Err("World snapshot has an invalid version or size.");
    }
    for entry in snapshot.blocks {
      if (!valid(entry.at, entry.material) || blocks.contains(entry.at)) {
        return Err("World snapshot has an invalid or duplicate block.");
      }
      blocks = blocks.insert(entry.at, entry.material);
    }
    revision = snapshot.revision;
  }
  let bytes: i64 = 0;
  if let Some(source) = text(base + ".journal")? {
    let offset: i32 = 0;
    for line in source.split("\n") {
      if (offset + line.len() >= source.len()) {
        break;
      }
      if (line == "") {
        return Err("World journal contains an empty record.");
      }
      let body = payload(line)?;
      let batch = json.decode[Batch](body).map_err((error: json.JsonError): string => error.message())?;
      if (batch.version != 1 || batch.changes.len() < 1 || batch.changes.len() > 512 || batch.revision < 1) {
        return Err("World journal has an invalid batch.");
      }
      if (batch.revision > revision) {
        if (batch.revision != revision + batch.changes.len() as i64) {
          return Err("World journal revision has a gap.");
        }
        for change in batch.changes {
          let at = model.key(change.x, change.y, change.z);
          if (change.x < 0 || change.x >= 512 || change.z < 0 || change.z >= 512 || !valid(at, change.material)) {
            return Err("World journal has an invalid block.");
          }
          if (change.material == voxel.terrain(change.x, change.y, change.z)) {
            blocks = blocks.remove(at);
          } else {
            blocks = blocks.insert(at, change.material);
          }
        }
        if (blocks.len() > model.MAX_EDITS) {
          return Err("World journal exceeds the block limit.");
        }
        revision = batch.revision;
      }
      offset = offset + line.len() + 1;
    }
    bytes = offset as i64;
    if (offset != source.len()) {
      trim_journal(base + ".journal", bytes)?;
    }
  }
  return Ok(Recovered { blocks: blocks, revision: revision, journal_bytes: bytes });
}
```

## Commit

The journal has a single owner. One batch needs one file synchronization, regardless of the number of participants who edited it.
Compaction runs after eight MiB of journal data. It publishes the snapshot before it truncates the journal.
Both possible crash orders recover the same confirmed blocks.

```fern
<<Commit>>=
pub function commit(store: Store, blocks: Map[i32, i32], changes: model.Change[], revision: i64): Result[void, string] {
  if (store.directory == "" || changes.len() == 0) {
    return Ok(());
  }
  let base = store.directory + "/" + store.name;
  let record = envelope(Batch { version: 1, revision: revision, changes: changes }.to_json());
  let writer = open_appender(base + ".journal").map_err((e: IoError): string => e.message())?;
  sync_close(writer, writer.write(record))?;
  if (store.bytes.get() == 0) {
    sync_directory(store.directory)?;
  }
  store.bytes.set(store.bytes.get() + record.len() as i64);
  if (store.bytes.get() >= 8388608) {
    compact(store, blocks, revision)?;
  }
  return Ok(());
}

pub function compact(store: Store, blocks: Map[i32, i32], revision: i64): Result[void, string] {
  if (store.directory == "") {
    return Ok(());
  }
  let base = store.directory + "/" + store.name;
  let entries: SavedBlock[] = [];
  for entry in blocks.entries() {
    entries = entries.append(SavedBlock { at: entry.0, material: entry.1 });
  }
  let snapshot = envelope(Snapshot { version: 1, revision: revision, blocks: entries }.to_json());
  atomic(base + ".snapshot", store.directory, snapshot)?;
  trim_journal(base + ".journal", 0)?;
  store.bytes.set(0);
  return Ok(());
}
```

# Fernbase: atomic SQL batches and an ordered index

This small SQL engine owns one table: `entries(key, value)`. Both columns contain text.
A request evaluates its complete batch on a private database value. An error discards that value.

The HTTP layer compares revisions before a write. A configured volume retains a synchronized checkpoint before publication.
A private draft uses the same engine without publication. Another commit makes that draft stale.

```fern
<<*>>=
<<Implementation>>
```

## Read the program

The named sections form one Fern module. The surrounding HTTP application calls these functions directly.

```fern
<<Implementation>>=
<<Database values>>
<<The primary index>>
<<SQL tokens>>
<<Parse one statement>>
<<Commit a batch>>
```

## Database values

Rows remain in primary-key order. Revisions identify committed batches.
The output separates selected rows, the query plan, comparison counts, and a change flag.

```fern
<<Database values>>=
import "std/array";
import "std/result";
import "std/option";
import "std/json";
import "std/string";
import "std/parse";
import "std/crypto";

@derive(json.Json, json.FromJson)
pub struct Row { key: string, value: string }

@derive(json.Json, json.FromJson)
pub struct Database { revision: i64, rows: Row[] }

@derive(json.Json)
pub struct Output { rows: Row[], plan: string, comparisons: i32, changed: boolean }

pub struct Execution { database: Database, output: Output }

pub function initial(): Database {
  return Database { revision: 0, rows: [] };
}
```

## The primary index

Binary search returns an insertion position and a comparison count. An absent key is an ordinary position.
An insert preserves key order. An update replaces one row. Duplicate inserts return a Result error.
Keys compare in UTF-8 byte order. The database holds at most 10000 rows.

```fern
<<The primary index>>=
pub function lower_bound(rows: Row[], key: string): (i32, i32) {
  let lo = 0;
  let hi = rows.len();
  let comparisons = 0;
  while (lo < hi) {
    let mid = lo + (hi - lo) / 2;
    comparisons = comparisons + 1;
    if (rows[mid].key < key) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return (lo, comparisons);
}

function put(rows: Row[], row: Row, insert_only: boolean): Result[Row[], string] {
  if (row.key.len() == 0 || row.key.len() > 128 || row.value.len() > 4096) {
    return Err("A key needs 1 to 128 bytes. A value has at most 4096 bytes.");
  }
  let (at, comparisons) = lower_bound(rows, row.key);
  if (at < rows.len() && rows[at].key == row.key) {
    if (insert_only) {
      return Err("The primary key already exists.");
    }
    return Ok(rows.with(at, row));
  }
  if (!insert_only) {
    return Err("The primary key does not exist.");
  }
  if (rows.len() >= 10000) {
    return Err("This database already has 10000 rows.");
  }
  let result: Row[] = [];
  let i = 0;
  while (i < at) {
    result = result.append(rows[i]);
    i = i + 1;
  }
  result = result.append(row);
  while (i < rows.len()) {
    result = result.append(rows[i]);
    i = i + 1;
  }
  return Ok(result);
}
```

## SQL tokens

The lexer preserves quoted Unicode text and doubled single quotes. A semicolon inside quoted text remains text.
Bare keywords use ASCII lowercase conversion. The lexer rejects an incomplete quote and input beyond 12000 bytes.

```fern
<<SQL tokens>>=
pub function tokens(sql: string): Result[string[], string] {
  if (sql.len() > 12000) {
    return Err("SQL exceeds 12000 bytes.");
  }
  let out: string[] = [];
  let at = 0;
  while (at < sql.len()) {
    let byte = sql[at];
    if (byte == 32 || byte == 10 || byte == 13 || byte == 9) {
      at = at + 1;
      continue;
    }
    if (byte == 39) {
      at = at + 1;
      let text = "";
      let closed = false;
      while (at < sql.len()) {
        let start = at;
        while (at < sql.len() && sql[at] != 39) {
          at = at + 1;
        }
        text = text + sql.drop(start).take(at - start);
        if (at >= sql.len()) {
          break;
        }
        at = at + 1;
        if (at < sql.len() && sql[at] == 39) {
          text = text + "'";
          at = at + 1;
        } else {
          closed = true;
          break;
        }
      }
      if (!closed) {
        return Err("Close the quoted SQL string.");
      }
      out = out.append("'" + text);
      continue;
    }
    if (byte == 40 || byte == 41 || byte == 44 || byte == 61 || byte == 59 || byte == 42) {
      out = out.append(sql.drop(at).take(1));
      at = at + 1;
      continue;
    }
    let start = at;
    while (at < sql.len() && sql[at] != 32 && sql[at] != 10 && sql[at] != 13 && sql[at] != 9 && sql[at] != 40 && sql[at] != 41 && sql[at] != 44 && sql[at] != 61 && sql[at] != 59) {
      at = at + 1;
    }
    out = out.append(sql.drop(start).take(at - start).to_ascii_lower());
  }
  return Ok(out);
}

function literal(token: string): Result[string, string] {
  if (!token.starts_with("'")) {
    return Err("Use single quotes around text values.");
  }
  return Ok(token.drop(1));
}
```

## Parse one statement

The parser accepts SELECT *, an equality WHERE clause, INSERT, UPDATE, DELETE, and EXPLAIN SELECT.
Unsupported syntax returns a Result error. EXPLAIN uses a private evaluation and never publishes a change.
A scan returns the first 200 rows. The export route returns the complete database.

```fern
<<Parse one statement>>=
function statement(db: Database, t: string[]): Result[Execution, string] {
  let output = Output { rows: [] as Row[], plan: "", comparisons: 0, changed: false };
  if (t.len() == 0) {
    return Ok(Execution { database: db, output: output });
  }
  if (t[0] == "explain") {
    if (t.len() < 2 || t[1] != "select") {
      return Err("EXPLAIN supports SELECT statements.");
    }
    let result = statement(db, t.drop(1))?;
    return Ok(Execution { database: db, output: Output { ...result.output, rows: [] as Row[], changed: false } });
  }
  if (t.len() >= 4 && t[0] == "select" && t[1] == "*" && t[2] == "from" && t[3] == "entries") {
    if (t.len() == 4) {
      return Ok(Execution { database: db, output: Output { ...output, rows: db.rows.take(200), plan: "ORDERED SCAN entries PRIMARY KEY, first 200 rows", comparisons: db.rows.len().min(200) } });
    }
    if (t.len() != 8 || t[4] != "where" || t[5] != "key" || t[6] != "=") {
      return Err("Use SELECT * FROM entries WHERE key = 'text'.");
    }
    let key = literal(t[7])?;
    let (at, comparisons) = lower_bound(db.rows, key);
    let rows: Row[] = [];
    if (at < db.rows.len() && db.rows[at].key == key) {
      rows = rows.append(db.rows[at]);
    }
    return Ok(Execution { database: db, output: Output { ...output, rows: rows, comparisons: comparisons, plan: "BINARY SEARCH entries PRIMARY KEY" } });
  }
  if (t.len() == 9 && t[0] == "insert" && t[1] == "into" && t[2] == "entries" && t[3] == "values" && t[4] == "(" && t[6] == "," && t[8] == ")") {
    let rows = put(db.rows, Row { key: literal(t[5])?, value: literal(t[7])? }, true)?;
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: true, plan: "UNIQUE PRIMARY KEY INSERT" } });
  }
  if (t.len() == 10 && t[0] == "update" && t[1] == "entries" && t[2] == "set" && t[3] == "value" && t[4] == "=" && t[6] == "where" && t[7] == "key" && t[8] == "=") {
    let rows = put(db.rows, Row { key: literal(t[9])?, value: literal(t[5])? }, false)?;
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: true, plan: "PRIMARY KEY UPDATE" } });
  }
  if (t.len() == 7 && t[0] == "delete" && t[1] == "from" && t[2] == "entries" && t[3] == "where" && t[4] == "key" && t[5] == "=") {
    let key = literal(t[6])?;
    let rows: Row[] = [];
    for row in db.rows {
      if (row.key != key) {
        rows = rows.append(row);
      }
    }
    return Ok(Execution { database: Database { ...db, rows: rows }, output: Output { ...output, changed: rows.len() != db.rows.len(), plan: "PRIMARY KEY DELETE" } });
  }
  return Err("Use SELECT, EXPLAIN SELECT, INSERT, UPDATE, or DELETE on entries(key, value).");
}
```

## Commit a batch

Each statement receives the private value from the previous statement. The original database remains unchanged.
The function publishes no state itself. It returns a candidate and increments its revision once for a changed batch.
The HTTP layer checks the revision and storage result before it installs that candidate.

```fern
<<Commit a batch>>=
pub function execute(database: Database, sql: string): Result[Execution, string] {
  let input = tokens(sql)?;
  let current: string[] = [];
  let db = database;
  let last = Output { rows: [] as Row[], plan: "No statements", comparisons: 0, changed: false };
  let changed = false;
  let count = 0;
  for token in input.append(";") {
    if (token == ";") {
      if (current.len() > 0) {
        count = count + 1;
        if (count > 128) {
          return Err("A transaction has at most 128 statements.");
        }
        let result = statement(db, current)?;
        db = result.database;
        last = result.output;
        changed = changed || last.changed;
        current = [];
      }
    } else {
      current = current.append(token);
    }
  }
  if (changed) {
    db = Database { ...db, revision: database.revision + 1 };
  }
  return Ok(Execution { database: db, output: Output { ...last, changed: changed } });
}
```

## Check the behavior

Run this example with `fern -doctest`. It uses the implementation above.

```fern test name=fernbase-example
<<Implementation>>
function main(): i32 {
 let original=initial();
 let Ok(result)=execute(original,"INSERT INTO entries VALUES ('fern','hello'); SELECT * FROM entries WHERE key='fern';") else { return 1; };
 if(original.rows.len()!=0 || result.output.rows.len()!=1 || result.database.revision!=1){return 2;}
 if(execute(result.database,"INSERT INTO entries VALUES ('other','draft'); INSERT INTO entries VALUES ('fern','duplicate');").is_ok()){return 3;}
 return 0;
}
```

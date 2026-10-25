# Lantern: an inverted index with BM25 ranking

The archive stores Unicode documents and an inverted index. A term points to document IDs and occurrence counts.
A query visits only the matching posting lists. Results include the number of entries that it visited.

Imports use one bounded transaction per document. NDJSON files stream from the browser with network backpressure.
Set `FERN_LABS_DATA` to retain the archive on a persistent volume.

```fern
<<*>>=
<<Implementation>>
```

## Read the program

The named sections form one Fern module. The surrounding HTTP application calls these functions directly.

```fern
<<Implementation>>=
<<Documents and postings>>
<<Unicode tokenization>>
<<Build the index>>
<<Rank a query>>
```

## Documents and postings

Document IDs are stable array positions. Each posting stores an ID and term frequency.
Lengths include the title and body. The index also retains byte and token counts.

```fern
<<Documents and postings>>=
import "std/array";
import "std/string";
import "std/result";
import "std/option";
import "std/json";
import "std/f64";
import "core/map";

@derive(json.Json, json.FromJson)
pub struct Document { id: i32, title: string, text: string }

pub struct Posting { id: i32, frequency: i32 }

pub struct Index { documents: Document[], lengths: i32[], terms: Map[string, Posting[]], words: i32, bytes: i32 }

@derive(json.Json)
pub struct Hit { id: i32, title: string, text: string, score: f64, matched: i32 }

@derive(json.Json)
pub struct Results { hits: Hit[], documents: i32, terms: i32, postings_read: i32, query_terms: string[] }

pub function empty(): Index {
  return Index { documents: [], lengths: [], terms: map_new(128), words: 0, bytes: 0 };
}
```

## Unicode tokenization

Unicode lowercase conversion precedes punctuation and whitespace tokenization.
Terms contain at most 128 UTF-8 bytes. CJK text without spaces remains one term.
The implementation shares this exact tokenizer between documents and queries.

```fern
<<Unicode tokenization>>=
pub function tokenize(text: string): string[] {
  let clean = text.to_lower();
  for separator in ["\n", "\r", "\t", ".", ",", ";", ":", "!", "?", "(", ")", "[", "]", "{", "}", "\"", "'", "/", "\\", "-", "—", "　"] {
    clean = clean.replace(separator, " ");
  }
  let terms: string[] = [];
  for term in clean.split(" ") {
    if (term.len() > 0 && term.len() <= 128) {
      terms = terms.append(term);
    }
  }
  return terms;
}
```

## Build the index

A new document first builds its term counts. Each distinct term then adds one posting.
The candidate index remains a private value until the HTTP layer confirms storage.
The archive holds at most 4096 documents, 4 MiB of text, 262144 tokens, and 65536 distinct terms.

```fern
<<Build the index>>=
pub function add(index: Index, document: Document): Result[Index, string] {
  if (index.documents.len() >= 4096 || document.id != index.documents.len() || document.title.len() == 0 || document.title.len() > 256 || document.text.len() > 8192) {
    return Err("Each document needs a title and at most 8192 bytes of text. The archive holds 4096 documents.");
  }
  if (index.bytes + document.title.len() + document.text.len() > 4194304) {
    return Err("The archive exceeds its 4 MiB text limit.");
  }
  let words = tokenize(document.title + " " + document.text);
  if (index.words + words.len() > 262144) {
    return Err("The archive exceeds its 262144-token limit.");
  }
  let counts: Map[string, i32] = map_new(128);
  for term in words {
    counts = counts.insert(term, counts.get(term).unwrap_or(0) + 1);
  }
  let terms = index.terms;
  for entry in counts.entries() {
    terms = terms.insert(entry.0, terms.get(entry.0).unwrap_or([] as Posting[]).append(Posting { id: document.id, frequency: entry.1 }));
  }
  if (terms.len() > 65536) {
    return Err("The archive exceeds its 65536-term vocabulary limit.");
  }
  return Ok(Index { documents: index.documents.append(document), lengths: index.lengths.append(words.len()), terms: terms, words: index.words + words.len(), bytes: index.bytes + document.title.len() + document.text.len() });
}
```

## Rank a query

BM25 uses k1 = 1.2 and b = 0.75. Inverse document frequency rewards rare terms.
Length normalization reduces the advantage of a long document. Repeated query terms count once.
A bounded selection retains twenty hits. Equal scores use document ID as the tie breaker.

```fern
<<Rank a query>>=
pub function search(index: Index, query: string): Results {
  let output: Hit[] = [];
  let seen: Map[string, boolean] = map_new(16);
  let terms: string[] = [];
  for term in tokenize(query.take(1024)) {
    if (!seen.contains(term) && terms.len() < 16) {
      seen = seen.insert(term, true);
      terms = terms.append(term);
    }
  }
  let scores: Map[i32, f64] = map_new(64);
  let matched: Map[i32, i32] = map_new(64);
  let visits = 0;
  let average = (index.words as f64 / (index.documents.len().max(1) as f64)).max(1.0);
  for term in terms {
    let postings = index.terms.get(term).unwrap_or([] as Posting[]);
    let idf = (1.0 + (index.documents.len() as f64 - postings.len() as f64 + 0.5) / (postings.len() as f64 + 0.5)).log();
    for posting in postings {
      visits = visits + 1;
      let frequency = posting.frequency as f64;
      let score = idf * frequency * 2.2 / (frequency + 1.2 * (0.25 + 0.75 * (index.lengths[posting.id] as f64) / average));
      scores = scores.insert(posting.id, scores.get(posting.id).unwrap_or(0.0) + score);
      matched = matched.insert(posting.id, matched.get(posting.id).unwrap_or(0) + 1);
    }
  }
  for entry in scores.entries() {
    let doc = index.documents[entry.0];
    let hit = Hit { id: doc.id, title: doc.title, text: doc.text.take(280), score: entry.1, matched: matched.get(doc.id).unwrap_or(0) };
    let next: Hit[] = [];
    let inserted = false;
    for old in output {
      if (!inserted && (hit.score > old.score || hit.score == old.score && hit.id < old.id)) {
        next = next.append(hit);
        inserted = true;
      }
      if (next.len() < 20) {
        next = next.append(old);
      }
    }
    if (!inserted && next.len() < 20) {
      next = next.append(hit);
    }
    output = next.take(20);
  }
  return Results { hits: output, documents: index.documents.len(), terms: index.terms.len(), postings_read: visits, query_terms: terms };
}
```

## Check the behavior

Run this example with `fern -doctest`. It uses the implementation above.

```fern test name=lantern-example
<<Implementation>>
function main(): i32 {
 let Ok(index)=add(empty(),Document {id:0,title:"A café",text:"Fern and 東京"}) else{return 1;};
 let result=search(index,"CAFÉ");
 if(result.hits.len()!=1 || result.hits[0].id!=0 || result.postings_read!=1){return 2;}
 return 0;
}
```

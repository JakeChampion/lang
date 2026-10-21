//! The Rust half of the map comparison (bench/maps/README.md). Each workload
//! builds a table of `n` keys once and then looks every key up `rounds` times
//! in a fixed random order, reporting nanoseconds per lookup; the same shapes
//! as the Fern programs beside it. Usage: `fern-maps-comparison <n> <rounds>`.

use std::collections::HashMap;
use std::hint::black_box;
use std::time::Instant;

use constmap::{ConstMap, VerifiedConstMap};
use multitable::MultiTable;
use rustc_hash::FxBuildHasher;

/// A fixed permutation of 0..n (splitmix64 + Fisher-Yates), so every table
/// answers the same queries in the same order.
fn order(n: usize) -> Vec<u32> {
    let mut v: Vec<u32> = (0..n as u32).collect();
    let mut s: u64 = 0x9E37_79B9_7F4A_7C15;
    for i in (1..n).rev() {
        s = s.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut z = s;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        z ^= z >> 31;
        let j = (z % (i as u64 + 1)) as usize;
        v.swap(i, j);
    }
    v
}

fn report(name: &str, n: usize, rounds: usize, elapsed_ns: u128, check: u64) {
    let per = elapsed_ns as f64 / (n * rounds) as f64;
    println!("{name}\tn={n}\tns_per_lookup={per:.1}\tchecksum={check}");
}

/// u32 keys `i * 7` with the value `i`, the shape of bench/map_int.fern.
fn int_keys(n: usize, rounds: usize, utilization: f64) {
    let keys: Vec<u32> = (0..n as u32).map(|i| i.wrapping_mul(7)).collect();
    let probe = order(n);

    let mut hb: hashbrown::HashMap<u32, u32, FxBuildHasher> = hashbrown::HashMap::with_capacity_and_hasher(n, FxBuildHasher);
    for (i, k) in keys.iter().enumerate() {
        hb.insert(*k, i as u32);
    }
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            sum = sum.wrapping_add(*hb.get(&keys[p as usize]).unwrap() as u64);
        }
    }
    report("hashbrown<u32,u32>", n, rounds, t.elapsed().as_nanos(), black_box(sum));

    // 4-byte keys and values, up to 8 levels, sized at `utilization`
    // (payload bytes over allocated bytes).
    let mut mt: MultiTable<4, 4, 8> = MultiTable::with_capacity(n as u64, utilization);
    for (i, k) in keys.iter().enumerate() {
        mt.insert(k.to_le_bytes(), (i as u32).to_le_bytes()).expect("multitable full");
    }
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            let v = mt.get(&keys[p as usize].to_le_bytes()).unwrap();
            sum = sum.wrapping_add(u32::from_le_bytes(*v) as u64);
        }
    }
    report(&format!("multitable<4,4> util={utilization}"), n, rounds, t.elapsed().as_nanos(), black_box(sum));
}

/// String keys `checker__symbol_<i>` with the value `i`, the shape of
/// bench/map_string.fern.
fn string_keys(n: usize, rounds: usize) {
    let owned: Vec<String> = (0..n).map(|i| format!("checker__symbol_{i}")).collect();
    let keys: Vec<&str> = owned.iter().map(String::as_str).collect();
    let values: Vec<u64> = (0..n as u64).collect();
    let probe = order(n);

    let mut std_map: HashMap<String, u64> = HashMap::with_capacity(n);
    for (k, v) in keys.iter().zip(&values) {
        std_map.insert((*k).to_string(), *v);
    }
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            sum = sum.wrapping_add(*std_map.get(keys[p as usize]).unwrap());
        }
    }
    report("std HashMap<String,u64>", n, rounds, t.elapsed().as_nanos(), black_box(sum));

    let mut hb: hashbrown::HashMap<&str, u64, FxBuildHasher> = hashbrown::HashMap::with_capacity_and_hasher(n, FxBuildHasher);
    for (k, v) in keys.iter().zip(&values) {
        hb.insert(*k, *v);
    }
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            sum = sum.wrapping_add(*hb.get(keys[p as usize]).unwrap());
        }
    }
    report("hashbrown<&str,u64> fx", n, rounds, t.elapsed().as_nanos(), black_box(sum));

    let t = Instant::now();
    let cm = ConstMap::new(&keys, &values).expect("constmap build");
    let build_ns = t.elapsed().as_nanos();
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            sum = sum.wrapping_add(cm.map(keys[p as usize]));
        }
    }
    report("constmap::ConstMap", n, rounds, t.elapsed().as_nanos(), black_box(sum));
    println!("constmap::ConstMap\tn={n}\tbuild_ms={:.1}", build_ns as f64 / 1e6);

    let vm = VerifiedConstMap::new(&keys, &values).expect("verified constmap build");
    let t = Instant::now();
    let mut sum: u64 = 0;
    for _ in 0..rounds {
        for &p in &probe {
            sum = sum.wrapping_add(vm.map(keys[p as usize]));
        }
    }
    report("constmap::VerifiedConstMap", n, rounds, t.elapsed().as_nanos(), black_box(sum));
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: usize = args.get(1).and_then(|s| s.parse().ok()).unwrap_or(4000);
    let rounds: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(40);
    let utilization: f64 = args.get(3).and_then(|s| s.parse().ok()).unwrap_or(0.9);
    int_keys(n, rounds, utilization);
    string_keys(n, rounds);
}

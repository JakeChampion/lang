package e2eharness

// EnumClosurePayloadProgram builds enum values that carry a closure in a
// payload, a concrete enum and a generic one at i32, 100 rounds over: eight
// it matches and calls through, eight whose owning local it overwrites
// inside the arm before calling the binding (the box must outlive the arm
// that borrows its payload), and eight it holds in an array, calls through
// from there, and drops whole. It exits 0 when every answer is right; a leak
// census over it must balance (#11349).
const EnumClosurePayloadProgram = `import "std/i32";
enum E { A(i32, (i32) => i32), B }
enum G[T] { P(T, (i32) => T), N }
function mk(n: i32): E {
    return A(n, (x: i32) => x + n);
}
function mkg(n: i32): G[i32] {
    return P(n, (x: i32) => x + n);
}
function apply_e(e: E): i32 {
    match (e) {
        A(k, f) => { return f(k); },
        B => { return 0; }
    }
}
function apply_g(g: G[i32]): i32 {
    match (g) {
        P(k, f) => { return f(k); },
        N => { return 0; }
    }
}
function overwrite_e(n: i32): i32 {
    let e: E = mk(n);
    let out: i32 = 0;
    match (e) {
        A(k, f) => {
            e = B;
            let pad: i32[] = [n, k, n, k, n, k, n, k];
            let tag: string = "abcdefghijklmnopqrstuvwxyz" + pad.len().to_string();
            out = f(k) + tag.len() - 27;
        },
        B => {}
    }
    return out;
}
function overwrite_g(n: i32): i32 {
    let g: G[i32] = mkg(n);
    let out: i32 = 0;
    match (g) {
        P(k, f) => {
            g = N;
            let pad: i32[] = [n, k, n, k, n, k, n, k];
            let tag: string = "abcdefghijklmnopqrstuvwxyz" + pad.len().to_string();
            out = f(k) + tag.len() - 27;
        },
        N => {}
    }
    return out;
}
function main(): i32 {
    let r: i32 = 0;
    let sum: i32 = 0;
    while (r < 100) {
        let i: i32 = 0;
        while (i < 8) {
            let e: E = mk(i);
            sum = sum + apply_e(e);
            let g: G[i32] = mkg(i);
            sum = sum + apply_g(g);
            sum = sum + overwrite_e(i) + overwrite_g(i);
            i = i + 1;
        }
        let es: E[] = [];
        let gs: G[i32][] = [];
        let j: i32 = 0;
        while (j < 8) {
            es = es.append(mk(j));
            gs = gs.append(mkg(j));
            j = j + 1;
        }
        j = 0;
        while (j < 8) {
            sum = sum + apply_e(es[j]) + apply_g(gs[j]);
            j = j + 1;
        }
        sum = sum + es.len() + gs.len();
        r = r + 1;
    }
    if (sum != 100 * (56 * 6 + 8 + 8)) {
        return 1;
    }
    return 0;
}`

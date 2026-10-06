package e2eharness

const ASCIIByteTextProgram = `import "std/i32";
function main(): i32 {
    let n: i32 = 0;
    while (n < 256) {
        let b: u8 = n as u8;
        let text = b.to_ascii_string();
        if (n < 128) {
            if (text.len() != 1 || text[0] != b) { return 1; }
        } else {
            if (text.len() != 0) { return 2; }
        }
        let other = text + "x";
        if (other.len() != text.len() + 1) { return 3; }
        n = n + 1;
    }
    return 0;
}
`

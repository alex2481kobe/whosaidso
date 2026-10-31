use super::process::cli;
use super::write;
use std::path::Path;

pub fn field(n: usize, bytes: &[u8]) -> Vec<u8> {
    fn varint(mut n: usize) -> Vec<u8> {
        let mut b = Vec::new();
        while n >= 128 {
            b.push(n as u8 | 128);
            n >>= 7;
        }
        b.push(n as u8);
        b
    }
    let mut b = varint(n * 8 + 2);
    b.extend(varint(bytes.len()));
    b.extend(bytes);
    b
}
pub fn doc(path: &str, symbol: &str, display: &str, ranges: &[(&[u8], bool)]) -> Vec<u8> {
    encoded_doc(path, symbol, display, ranges, 0)
}
pub fn encoded_doc(
    path: &str,
    symbol: &str,
    display: &str,
    ranges: &[(&[u8], bool)],
    encoding: u8,
) -> Vec<u8> {
    let mut d = field(1, path.as_bytes());
    d.extend([48, encoding]);
    for (range, def) in ranges {
        let mut o = field(1, range);
        o.extend(field(2, symbol.as_bytes()));
        o.extend([24, u8::from(*def)]);
        d.extend(field(2, &o));
    }
    let mut info = field(1, symbol.as_bytes());
    info.extend(field(6, display.as_bytes()));
    d.extend(field(3, &info));
    field(2, &d)
}
pub fn import(root: &Path, docs: &[Vec<u8>]) -> String {
    let mut index = field(1, &[]);
    for d in docs {
        index.extend(d);
    }
    write(&root.join(".git/index.scip"), index);
    cli(root, &["code", "import-scip", ".git/index.scip"])
}

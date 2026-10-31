// SCIP descriptor names, including backquoted identifiers.
// SCIP's descriptor grammar: a backquoted name is one name, and a doubled backtick inside it is a literal one.
pub(super) fn descriptors(symbol: &str) -> Vec<String> {
    let b = symbol.as_bytes();
    let (mut i, mut spaces) = (0, 0);
    while i < b.len() && spaces < 4 {
        if b[i] == b' ' {
            if i + 1 < b.len() && b[i + 1] == b' ' {
                i += 1;
            } else {
                spaces += 1;
            }
        }
        i += 1;
    }
    if spaces != 4 {
        return Vec::new();
    }
    let mut names = Vec::new();
    while i < b.len() {
        let part;
        if b[i] == b'(' || b[i] == b'[' {
            let end = if b[i] == b'(' { b')' } else { b']' };
            i += 1;
            let start = i;
            while i < b.len() && b[i] != end {
                i += 1;
            }
            part = symbol[start..i].to_string();
            i += usize::from(i < b.len());
        } else {
            let start = i;
            let mut quoted = false;
            while i < b.len() {
                if b[i] == b'`' {
                    if quoted && i + 1 < b.len() && b[i + 1] == b'`' {
                        i += 2;
                        continue;
                    }
                    quoted = !quoted;
                    i += 1;
                    continue;
                }
                if !quoted && b"/#.:!([".contains(&b[i]) {
                    break;
                }
                i += 1;
            }
            part = symbol[start..i]
                .replace("``", "\0")
                .replace('`', "")
                .replace('\0', "`");
            if i < b.len() && b[i] == b'(' {
                while i < b.len() && b[i] != b')' {
                    i += 1;
                }
                i += usize::from(i < b.len());
            }
            if i < b.len() {
                i += 1;
            }
        }
        if !part.is_empty() {
            names.push(part);
        }
    }
    names
}
pub(super) fn name(symbol: &str) -> String {
    descriptors(symbol).pop().unwrap_or_default()
}

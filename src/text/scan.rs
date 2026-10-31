// Live byte matching.
use super::options::Options;
use regex::bytes::Regex;

pub(super) fn bytes(
    path: &str,
    bytes: &[u8],
    regex: &Regex,
    options: &Options,
    cap: usize,
) -> (Vec<String>, usize) {
    if bytes.is_empty() || bytes[..bytes.len().min(8000)].contains(&0) {
        return (Vec::new(), 0);
    }
    if options.matching_files {
        return if regex.is_match(bytes) {
            (
                if cap > 0 {
                    vec![path.to_owned()]
                } else {
                    Vec::new()
                },
                1,
            )
        } else {
            (Vec::new(), 0)
        };
    }
    let mut rows = Vec::new();
    let mut total = 0;
    let (mut cursor, mut line) = (0, 1);
    while cursor < bytes.len() {
        let Some(found) = regex.find_at(bytes, cursor) else {
            break;
        };
        if found.start() == bytes.len() {
            break;
        }
        let end = bytes[found.start()..]
            .iter()
            .position(|b| *b == b'\n')
            .map_or(bytes.len(), |i| found.start() + i);
        total += 1;
        if rows.len() < cap {
            line += bytes[cursor..found.start()]
                .iter()
                .filter(|b| **b == b'\n')
                .count();
            let start = bytes[cursor..found.start()]
                .iter()
                .rposition(|b| *b == b'\n')
                .map_or(cursor, |i| cursor + i + 1);
            let text = String::from_utf8_lossy(&bytes[start..end]);
            rows.push(format!(
                "{path}:{line}:{}",
                text.strip_suffix('\r').unwrap_or(&text)
            ));
        }
        cursor = end + 1;
        line += 1;
    }
    (rows, total)
}

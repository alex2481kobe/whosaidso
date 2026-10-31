// Bounded live-file scanning, with Git fallback for incompatible bytes.
use super::options::Options;
use regex::bytes::Regex;
use std::{io::Read, path::Path};

const BLOCK: usize = 1024 * 1024;
pub(super) struct Scanner {
    bytes: Vec<u8>,
    chunk: [u8; 64 * 1024],
}
impl Scanner {
    pub fn new() -> Self {
        Self {
            bytes: Vec::with_capacity(BLOCK + 64 * 1024),
            chunk: [0; 64 * 1024],
        }
    }
    pub fn file(
        &mut self,
        root: &Path,
        path: &str,
        regex: &Regex,
        options: &Options,
        cap: usize,
    ) -> Result<Option<(Vec<String>, usize)>, String> {
        let mut input = std::fs::File::open(root.join(path)).map_err(|e| format!("{path}: {e}"))?;
        self.bytes.clear();
        let (mut rows, mut total, mut line) = (Vec::new(), 0, 1);
        loop {
            let n = input
                .read(&mut self.chunk)
                .map_err(|e| format!("{path}: {e}"))?;
            self.bytes.extend_from_slice(&self.chunk[..n]);
            if n > 0 && self.bytes.len() < BLOCK {
                continue;
            }
            let end = if n == 0 {
                self.bytes.len()
            } else {
                let Some(end) = self.bytes.iter().rposition(|b| *b == b'\n') else {
                    return Ok(None);
                };
                end + 1
            };
            let part = &self.bytes[..end];
            if !super::matcher::byte_folding(options, part) || part.contains(&0) {
                return Ok(None);
            }
            if !options.matching_files || total == 0 {
                let (found, count) = bytes(
                    path,
                    part,
                    regex,
                    options,
                    cap.saturating_sub(rows.len()),
                    line,
                );
                rows.extend(found);
                total += count;
            }
            if rows.len() < cap {
                line += part.iter().filter(|b| **b == b'\n').count();
            }
            self.bytes.copy_within(end.., 0);
            self.bytes.truncate(self.bytes.len() - end);
            if n == 0 {
                break;
            }
        }
        Ok(Some((rows, total)))
    }
}

fn bytes(
    path: &str,
    bytes: &[u8],
    regex: &Regex,
    options: &Options,
    cap: usize,
    first_line: usize,
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
    let (mut cursor, mut line) = (0, first_line);
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

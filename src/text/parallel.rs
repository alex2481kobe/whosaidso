// Only the ASCII subset shared by Git ERE and the byte matcher is accelerated.
use super::options::Options;
use regex::bytes::{Regex, RegexBuilder};
use std::{
    path::Path,
    sync::atomic::{AtomicUsize, Ordering},
};

pub(super) fn matcher(options: &Options) -> Option<Regex> {
    if options.pattern.contains("(?") {
        return None;
    }
    if options.context != 0
        || !options.pattern.is_ascii()
        || options.pattern.contains(['\n', '\r', '\0'])
    {
        return None;
    }
    if options.ignore_case {
        let locale = ["LC_ALL", "LC_CTYPE", "LANG"]
            .into_iter()
            .filter_map(|k| std::env::var(k).ok())
            .find(|v| !v.is_empty())
            .unwrap_or_default();
        if !(locale.is_empty()
            || locale == "POSIX"
            || locale == "C"
            || locale.starts_with("C.")
            || locale.starts_with("en_"))
        {
            return None;
        }
    }
    let pattern = if options.regex {
        if options
            .pattern
            .as_bytes()
            .windows(2)
            .any(|w| w.iter().all(|b| b"*+?".contains(b)))
        {
            return None;
        }
        let mut class = false;
        for b in options.pattern.bytes() {
            if b == b'[' && !class {
                class = true;
            } else if b == b']' && class {
                class = false;
            } else if !(b.is_ascii_alphanumeric()
                || b"_ -".contains(&b)
                || (!class && b".*+?|()^$".contains(&b)))
            {
                return None;
            }
        }
        if class {
            return None;
        }
        options.pattern.clone()
    } else {
        regex::escape(&options.pattern)
    };
    RegexBuilder::new(&pattern)
        .unicode(false)
        .multi_line(true)
        .case_insensitive(options.ignore_case)
        .build()
        .ok()
}

// A leading literal is mandatory unless alternation or optional repetition removes it.
pub(super) fn prefix(pattern: &str) -> &str {
    if pattern.contains('|') {
        return "";
    }
    let pattern = pattern.strip_prefix('^').unwrap_or(pattern);
    let mut end = pattern
        .bytes()
        .take_while(|b| b.is_ascii_alphanumeric() || b"_ -".contains(b))
        .count();
    if matches!(pattern.as_bytes().get(end), Some(b'*' | b'?' | b'{')) {
        end = end.saturating_sub(1);
    }
    &pattern[..end]
}

fn scan(
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

pub(super) fn run(
    root: &Path,
    paths: &[String],
    regex: &Regex,
    options: &Options,
) -> Result<(Vec<String>, usize, Vec<String>), String> {
    let special = super::attributes::special(root, paths)?;
    let next = AtomicUsize::new(0);
    let cap = options.offset.saturating_add(options.limit);
    let threads = std::thread::available_parallelism()
        .map_or(1, usize::from)
        .min(8)
        .min(paths.len());
    let mut results = std::thread::scope(|scope| {
        let workers: Vec<_> = (0..threads)
            .map(|_| {
                scope.spawn(|| {
                    let regex = regex.clone();
                    let mut results = Vec::new();
                    loop {
                        let start = next.fetch_add(64, Ordering::Relaxed);
                        if start >= paths.len() {
                            break;
                        }
                        let mut rows = Vec::new();
                        let mut total = 0;
                        let mut fallback = Vec::new();
                        for (i, path) in paths.iter().enumerate().skip(start).take(64) {
                            if special.contains(path) {
                                fallback.push(i);
                                continue;
                            }
                            let bytes = std::fs::read(root.join(path))
                                .map_err(|e| format!("{path}: {e}"))?;
                            if ((options.ignore_case || options.regex) && !bytes.is_ascii())
                                || (bytes.len() > 8000 && bytes[8000..].contains(&0))
                            {
                                fallback.push(i);
                            } else {
                                let (found, count) = scan(
                                    path,
                                    &bytes,
                                    &regex,
                                    options,
                                    cap.saturating_sub(rows.len()),
                                );
                                rows.extend(found);
                                total += count;
                            }
                        }
                        results.push((start, rows, total, fallback));
                    }
                    Ok::<_, String>(results)
                })
            })
            .collect();
        let mut results = Vec::new();
        for worker in workers {
            results.extend(worker.join().map_err(|_| "text search thread failed")??);
        }
        Ok::<_, String>(results)
    })?;
    results.sort_unstable_by_key(|(i, _, _, _)| *i);
    let (mut rows, mut total, mut fallback) = (Vec::new(), 0, Vec::new());
    for (_, found, count, missing) in results {
        let remaining = cap.saturating_sub(rows.len());
        rows.extend(found.into_iter().take(remaining));
        total += count;
        fallback.extend(missing.into_iter().map(|i| paths[i].clone()));
    }
    Ok((rows, total, fallback))
}

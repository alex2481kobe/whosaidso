// Only the ASCII subset shared by Git ERE and the byte matcher is accelerated.
use super::options::Options;
use regex::bytes::Regex;
use std::{
    path::Path,
    sync::atomic::{AtomicUsize, Ordering},
};

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
                                let (found, count) = super::scan::bytes(
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

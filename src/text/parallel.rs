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
    let disk_root = if std::env::current_dir().is_ok_and(|cwd| cwd == root) {
        Path::new(".")
    } else {
        root
    };
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
                    let mut remaining = cap;
                    let mut scanner = super::scan::Scanner::new();
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
                            if let Some((found, count)) = scanner.file(
                                disk_root,
                                path,
                                &regex,
                                options,
                                remaining.saturating_sub(rows.len()),
                            )? {
                                rows.extend(found);
                                total += count;
                            } else {
                                fallback.push(i);
                            }
                        }
                        remaining -= rows.len();
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

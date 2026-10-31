use super::{git, index, matcher, options::Options, parallel};
use std::path::Path;

pub(super) fn run(root: &Path, options: &Options) -> Result<(Vec<String>, usize), String> {
    if options.files {
        return git::listing(root, options).map(complete);
    }
    let matcher = if super::attributes::custom_output(root)? {
        None
    } else {
        matcher::compile(options)
    };
    let mut candidates = if options.regex {
        if matcher.is_some() {
            index::candidates(root, matcher::prefix(&options.pattern))?
        } else {
            None
        }
    } else {
        index::candidates(root, &options.pattern)?
    };
    if !options.paths.is_empty() || !options.globs.is_empty() {
        let paths = git::listing(root, options)?;
        candidates = Some(match candidates {
            Some(c) => paths
                .into_iter()
                .filter(|p| c.binary_search(p).is_ok())
                .collect(),
            None => paths,
        });
    }
    if let Some(paths) = &mut candidates {
        paths.retain(|p| std::fs::symlink_metadata(root.join(p)).is_ok_and(|m| m.is_file()));
    }
    // --exclude-standard also applies to tracked paths in Git's untracked grep walk.
    if let Some(paths) = &mut candidates {
        let ignored = super::attributes::ignored(root, paths)?;
        paths.retain(|p| !ignored.contains(p));
    }
    if candidates.as_ref().is_some_and(Vec::is_empty) {
        if options.regex {
            git::validate(root, options)?;
        }
        return Ok((Vec::new(), 0));
    }
    if let Some(matcher) = matcher {
        let paths = match candidates {
            Some(paths) => paths,
            None => {
                let mut paths = git::listing(root, options)?;
                let aliases = super::attributes::aliases(root, paths.iter().cloned())?;
                for path in &mut paths {
                    if let Some(actual) = aliases.get(path) {
                        *path = actual.clone();
                    }
                }
                paths.sort();
                paths.dedup();
                let ignored = super::attributes::ignored(root, &paths)?;
                paths.retain(|p| !ignored.contains(p));
                paths
            }
        };
        if paths
            .iter()
            .any(|p| p.bytes().any(|b| b < 32 || b"\"\\:".contains(&b)))
        {
            return git::batches(root, options, &paths);
        }
        let (mut rows, total, fallback) = parallel::run(root, &paths, &matcher, options)?;
        let (missing, count) = git::batches(root, options, &fallback)?;
        let total = total + count;
        rows.extend(missing);
        // Rows from each path retain numeric line order, including 10 after 9.
        rows.sort_by(|a, b| {
            if options.matching_files {
                a.cmp(b)
            } else {
                a.split_once(':')
                    .unwrap()
                    .0
                    .cmp(b.split_once(':').unwrap().0)
            }
        });
        rows.truncate(options.offset.saturating_add(options.limit));
        return Ok((rows, total));
    }
    if let Some(paths) = candidates {
        git::batches(root, options, &paths)
    } else {
        git::run(
            root,
            options,
            None,
            options.offset.saturating_add(options.limit),
        )
    }
}
fn complete(rows: Vec<String>) -> (Vec<String>, usize) {
    let total = rows.len();
    (rows, total)
}

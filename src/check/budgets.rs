// Record retirement and test/check budgets; diagnostics name concrete cleanup work.
use crate::{config, git, note::model::Status, record::model::Store};
use std::{collections::HashSet, fs, path::Path};
pub fn problems(root: &Path, store: &Store) -> Result<Vec<String>, String> {
    let config = config::load(root)?;
    let mut problems = Vec::new();
    for goal in &store.goals {
        let relevant: Vec<_> = store
            .notes
            .iter()
            .filter(|n| {
                n.header.goal.as_ref() == Some(&goal.id)
                    && n.header.status != Status::Retired
                    && root.join(format!(".whosaidso/notes/{}.md", n.id)).exists()
            })
            .collect();
        if relevant.len() > config.limits.notes {
            let mut notes = Vec::new();
            for n in relevant {
                let history = git::run(
                    root,
                    &[
                        "log",
                        "--reverse",
                        "--format=%H",
                        "--",
                        &format!(".whosaidso/notes/{}.md", n.id),
                    ],
                )?;
                notes.push((history.lines().next().unwrap_or("~").to_owned(), &n.id));
            }
            // Git's topological order disambiguates creations sharing a wall-clock second.
            let history = git::run(root, &["rev-list", "--reverse", "HEAD"])?;
            notes.sort_by_key(|(commit, id)| {
                (
                    history
                        .lines()
                        .position(|c| c == commit)
                        .unwrap_or(usize::MAX),
                    *id,
                )
            });
            if notes.len() > config.limits.notes {
                problems.push(format!(
                    "limits.notes {}: goal {} has {}; retire {}",
                    config.limits.notes,
                    goal.id,
                    notes.len(),
                    notes
                        .iter()
                        .take(notes.len() - config.limits.notes)
                        .map(|(_, id)| id.as_str())
                        .collect::<Vec<_>>()
                        .join(", ")
                ));
            }
        }
        let mut proofs: Vec<_> = store
            .proofs
            .iter()
            .filter(|p| p.goal == goal.id && !store.folded.contains_key(&goal.id))
            .collect();
        proofs.sort_by_key(|p| (p.ran_at, &p.sha));
        if proofs.len() > config.limits.proofs {
            problems.push(format!(
                "limits.proofs {}: goal {} has {}; retire {} with prune",
                config.limits.proofs,
                goal.id,
                proofs.len(),
                proofs[0].sha
            ));
        }
        if let Some(limit) = config.limits.check_seconds.value() {
            for p in proofs {
                if p.timed_out || p.elapsed_ms as f64 > limit * 1000.0 {
                    problems.push(format!(
                        "limits.check_seconds {limit}: slow check {} ({} ms)",
                        goal.id, p.elapsed_ms
                    ));
                }
            }
        }
    }
    if let Some(ratio) = config.limits.tests.value() {
        problems.extend(test_size(root, &config.test_paths, ratio)?);
    }
    Ok(problems)
}
fn listing(root: &Path, patterns: &[String]) -> Result<Vec<String>, String> {
    let mut args = vec![
        "ls-files",
        "-z",
        "--cached",
        "--others",
        "--exclude-standard",
        "--",
    ];
    args.extend(patterns.iter().map(String::as_str));
    Ok(git::run(root, &args)?
        .split('\0')
        .filter(|p| !p.is_empty())
        .map(str::to_owned)
        .collect())
}
fn test_size(root: &Path, globs: &[String], ratio: f64) -> Result<Vec<String>, String> {
    let patterns: Vec<_> = globs.iter().map(|g| format!(":(glob){g}")).collect();
    let tests: HashSet<_> = if patterns.is_empty() {
        HashSet::new()
    } else {
        listing(root, &patterns)?.into_iter().collect()
    };
    let mut test_files = Vec::new();
    let mut source = 0;
    for path in listing(root, &[".".into(), ":(exclude).whosaidso".into()])? {
        let meta = match fs::symlink_metadata(root.join(&path)) {
            Ok(m) => m,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(e) => return Err(e.to_string()),
        };
        if !meta.is_file() || meta.file_type().is_symlink() {
            continue;
        }
        if tests.contains(&path) {
            test_files.push((meta.len(), path));
        } else if Path::new(&path)
            .extension()
            .and_then(|e| e.to_str())
            .is_some_and(|e| {
                [
                    "rs", "go", "py", "ts", "tsx", "js", "jsx", "c", "h", "cpp", "hpp", "cc",
                    "cxx", "swift", "java", "kt", "cs", "rb", "php", "css", "html", "sh",
                ]
                .contains(&e)
            })
        {
            source += meta.len();
        }
    }
    let count: u64 = test_files.iter().map(|(n, _)| n).sum();
    if count as f64 <= source as f64 * ratio {
        return Ok(Vec::new());
    }
    test_files.sort_by(|a, b| b.cmp(a));
    Ok(vec![format!(
        "limits.tests {ratio}: {count} test bytes / {source} source bytes; largest: {}",
        test_files
            .iter()
            .take(3)
            .map(|(n, p)| format!("{p} ({n})"))
            .collect::<Vec<_>>()
            .join(", ")
    )])
}

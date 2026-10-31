use crate::{config, git, record};
use std::{collections::BTreeSet, fs, path::Path};
pub(super) fn goals(root: &Path, baseline: &str) -> Result<Vec<String>, String> {
    let log = git::run(
        root,
        &[
            "log",
            "--no-merges",
            "--format=%(trailers:key=Goal,valueonly)",
            &format!("{baseline}..HEAD"),
        ],
    )?;
    let mut ids: BTreeSet<String> = log
        .lines()
        .filter(|s| !s.is_empty() && *s != "none")
        .map(str::to_owned)
        .collect();
    let selection =
        root.join(git::run(root, &["rev-parse", "--git-path", "whosaidso-goal"])?.trim());
    if let Ok(id) = fs::read_to_string(selection)
        && record::valid_id(id.trim())
        && id.trim() != "none"
    {
        ids.insert(id.trim().into());
    }
    Ok(ids.into_iter().collect())
}

pub(super) fn limits(root: &Path, baseline: &str, named: Option<&str>) -> Result<(), String> {
    let Some(limit) = config::load(root)?.limits.land_lines.value() else {
        return Ok(());
    };
    let stat = git::run(
        root,
        &[
            "diff",
            "--numstat",
            baseline,
            "HEAD",
            "--",
            ".",
            ":(exclude).whosaidso",
        ],
    )?;
    let mut count = 0.0;
    for line in stat.lines() {
        let fields: Vec<_> = line.splitn(3, '\t').collect();
        for field in fields.iter().take(2) {
            count += field.parse::<f64>().unwrap_or(f64::INFINITY);
        }
    }
    if count > limit && named.is_none() {
        return Err(format!(
            "limits.land_lines {limit}: {count} changed lines; use --override <name> for an intentional exception"
        ));
    }
    Ok(())
}

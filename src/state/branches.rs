use super::Branch;
use crate::{git, record::model::Store};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};
// Local names shadow the same branch on every remote, even if the local tip is older.
fn branch_refs(root: &Path) -> Result<Vec<(String, String)>, String> {
    let refs = git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(refname)",
            "refs/heads",
            "refs/remotes",
        ],
    )?;
    let main = crate::config::main(root)?;
    let local: HashSet<_> = refs
        .lines()
        .filter_map(|r| r.strip_prefix("refs/heads/"))
        .collect();
    let mut selected = Vec::new();
    for reference in refs.lines() {
        let name = if let Some(name) = reference.strip_prefix("refs/heads/") {
            if name == main {
                continue;
            }
            name
        } else if let Some(name) = reference.strip_prefix("refs/remotes/") {
            let Some((_, branch)) = name.split_once('/') else {
                continue;
            };
            if branch == "HEAD" || branch == main || local.contains(branch) {
                continue;
            }
            name
        } else {
            continue;
        };
        selected.push((name.to_owned(), reference.to_owned()));
    }
    selected.sort();
    Ok(selected)
}
fn worktrees(root: &Path) -> Result<HashMap<String, String>, String> {
    let listing = git::run(root, &["worktree", "list", "--porcelain", "-z"])?;
    let mut paths = HashMap::new();
    let mut path = "";
    for field in listing.split('\0') {
        if let Some(value) = field.strip_prefix("worktree ") {
            path = value;
        }
        if let Some(branch) = field.strip_prefix("branch refs/heads/") {
            paths.insert(branch.to_owned(), path.to_owned());
        }
    }
    Ok(paths)
}

pub(super) fn collect(
    root: &Path,
    store: &Store,
    tip: &str,
) -> Result<HashMap<String, Vec<Branch>>, String> {
    let mut branches: HashMap<String, Vec<Branch>> = HashMap::new();
    let goals: HashSet<_> = store.goals.iter().map(|g| g.id.as_str()).collect();
    let mut paths = None;
    for (name, reference) in branch_refs(root)? {
        let range = format!("{tip}...{reference}");
        let log = super::work_log(
            root,
            &[
                "log",
                "--no-merges",
                "--cherry-pick",
                "--right-only",
                "--format=%H%x00%ct%x00%an%x00%(trailers:key=Goal,valueonly,separator=%x2C)",
                &range,
            ],
        )?;
        let mut newest: HashMap<&str, (i64, &str)> = HashMap::new();
        for line in log.lines() {
            let parts: Vec<_> = line.splitn(4, '\0').collect();
            if let [_, time, author, goal] = parts.as_slice() {
                if !goals.contains(goal) {
                    continue;
                }
                if let Ok(time) = time.parse::<i64>() {
                    let entry = newest.entry(goal).or_insert((time, author));
                    if time > entry.0 {
                        *entry = (time, author);
                    }
                }
            }
        }
        let local = reference.starts_with("refs/heads/");
        if local && !newest.is_empty() && paths.is_none() {
            paths = Some(worktrees(root)?);
        }
        for (goal, (time, author)) in newest {
            let worktree = if local {
                paths.as_ref().and_then(|p| p.get(&name)).cloned()
            } else {
                None
            };
            branches.entry(goal.to_owned()).or_default().push(Branch {
                name: name.clone(),
                author: author.to_owned(),
                time,
                worktree,
            });
        }
    }
    Ok(branches)
}

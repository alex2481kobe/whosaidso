use super::{LOCAL, ORIGIN, storage::*};
use crate::{git, state};
use std::path::Path;
// A held claim as the viewer shows it.
#[derive(serde::Serialize)]
pub struct Held {
    pub goal: String,
    pub worker: String,
    pub branch: String,
    pub author: String,
    pub path: String,
    pub since: i64,
}
// Every claim that still binds, goal by goal; origin's claim wins over this clone's, as in `holder`.
pub fn held(root: &Path, states: &[state::GoalState], now: i64) -> Result<Vec<Held>, String> {
    let listing = git::run(
        root,
        &[
            "for-each-ref",
            "--format=%(refname) %(objectname)",
            ORIGIN,
            LOCAL,
        ],
    )?;
    let rows: Vec<(bool, &str, &str)> = listing
        .lines()
        .filter_map(|line| {
            let (reference, oid) = line.split_once(' ')?;
            Some(match reference.strip_prefix(ORIGIN) {
                Some(id) => (true, id, oid),
                None => (false, reference.strip_prefix(LOCAL)?, oid),
            })
        })
        .collect();
    let names: Vec<String> = rows.iter().map(|r| r.2.to_owned()).collect();
    let mut claims: std::collections::BTreeMap<&str, (Option<Claim>, Option<Claim>)> =
        std::collections::BTreeMap::new();
    git::read_objects(root, &names, 4096, |i, bytes, _| {
        let (from_origin, id, oid) = rows[i];
        let claim = bytes.map(|b| parse(oid.to_owned(), &String::from_utf8_lossy(&b)));
        let entry = claims.entry(id).or_default();
        if from_origin {
            entry.0 = claim
        } else {
            entry.1 = claim
        }
        Ok(())
    })?;
    Ok(claims
        .into_iter()
        .filter_map(|(id, (origin, local))| {
            let c = origin.or(local)?;
            let settled = states
                .iter()
                .find(|s| s.id == id)
                .is_some_and(|s| s.state.settled());
            binds(&c, now, settled).then(|| Held {
                goal: id.to_owned(),
                worker: c.worker,
                branch: c.branch,
                author: c.author,
                path: c.path,
                since: c.since,
            })
        })
        .collect())
}

// Claim identity and worktree matching are shared rules, not client-side guesses.
#[derive(serde::Serialize)]
pub struct Work<'a> {
    pub name: &'a str,
    pub author: &'a str,
    pub time: i64,
    pub path: Option<&'a str>,
    pub claimed: bool,
}
pub fn work<'a>(branches: &'a [state::Branch], claim: Option<&'a Held>) -> Vec<Work<'a>> {
    let held = claim.and_then(|c| {
        branches.iter().position(|b| {
            (b.name == c.branch || b.name.ends_with(&format!("/{}", c.branch)))
                && b.worktree
                    .as_deref()
                    .is_none_or(|p| p.is_empty() || p == c.path)
        })
    });
    let mut work: Vec<_> = branches
        .iter()
        .enumerate()
        .map(|(i, b)| {
            let claimed = held == Some(i);
            let path = if claimed {
                b.worktree
                    .as_deref()
                    .filter(|p| !p.is_empty())
                    .or_else(|| claim.map(|c| c.path.as_str()))
            } else {
                b.worktree.as_deref()
            };
            Work {
                name: &b.name,
                author: &b.author,
                time: b.time,
                path,
                claimed,
            }
        })
        .collect();
    if held.is_none()
        && let Some(c) = claim
    {
        work.push(Work {
            name: &c.branch,
            author: &c.author,
            time: c.since,
            path: Some(&c.path),
            claimed: true,
        });
    }
    work
}

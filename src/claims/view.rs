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

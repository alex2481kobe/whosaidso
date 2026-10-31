use super::{err, put, remove, stamp};
use crate::git;
use rusqlite::Transaction;
use std::path::Path;
pub(super) fn sync(root: &Path, tx: &Transaction<'_>) -> Result<(), String> {
    let head = git::main_tip(root).ok().map(|s| s.trim().to_owned());
    let old = stamp(tx, "git:main")?;
    let ancestor = match (&old, &head) {
        (Some(old), Some(head)) if old != head => {
            git::run(root, &["merge-base", "--is-ancestor", old, head]).is_ok()
        }
        _ => false,
    };
    if head.is_none() || (head != old && !ancestor) {
        let keys: Vec<String> = tx
            .prepare("SELECT key FROM docs WHERE kind='commit'")
            .map_err(err)?
            .query_map([], |r| r.get(0))
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?;
        for key in keys {
            remove(tx, &key)?;
        }
        super::store::forget(tx, "git:main")?;
    }
    if let Some(head) = head
        && old.as_deref() != Some(&head)
    {
        let range = if ancestor {
            format!("{}..{head}", old.as_deref().unwrap())
        } else {
            head.clone()
        };
        let log = git::run(
            root,
            &[
                "log",
                "--no-merges",
                "--format=%H%x09%s%x09%(trailers:key=Goal,valueonly,separator=%x2C)",
                &range,
            ],
        )?;
        for line in log.lines() {
            let mut parts = line.splitn(3, '\t');
            let sha = parts.next().ok_or("invalid log")?;
            let title = parts.next().ok_or("invalid log")?;
            let goal = parts.next().unwrap_or("");
            let body = if goal.is_empty() {
                title.to_string()
            } else {
                format!("{title} goal {goal}")
            };
            put(tx, "commit", &format!("commit:{sha}"), title, &body)?;
        }
        super::store::set_stamp(tx, "git:main", &head)?;
    }
    Ok(())
}

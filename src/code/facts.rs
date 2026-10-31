// Complete, generation-bound reads for code consumers. A Snapshot owns its read transaction.
use super::{precise, store};
use crate::tree;
use rusqlite::Connection;
use std::path::Path;

mod bodies;
pub mod coverage;
pub mod edges;
pub mod files;
pub mod impact;
mod imports;
pub mod reads;
pub mod relationships;

pub struct Snapshot {
    pub id: String,
    pub change_token: String,
    pub project: String,
    pub worktree: String,
    pub files: tree::Files,
    pub(crate) db: Connection,
}
impl Snapshot {
    pub fn current(root: &Path) -> Result<Self, String> {
        let (db, _, _) = store::indexed(root)?;
        Self::from_db(db)
    }
    pub fn published(root: &Path) -> Result<Self, String> {
        let db = store::open(root)?;
        Self::from_db(db)
    }
    pub fn branch(root: &Path, branch: &str) -> Result<Self, String> {
        Self::from_db(store::branch::open_snapshot(root, branch)?)
    }
    fn from_db(db: Connection) -> Result<Self, String> {
        precise::storage::schema(&db)?;
        db.execute_batch("BEGIN DEFERRED").map_err(err)?;
        store::snapshot(&db)?;
        let (generation, stamp, worktree): (i64,String,String) = db.query_row("SELECT g.id,g.stamp,coalesce(w.token,'branch:'||g.branch) FROM generations g LEFT JOIN worktrees w ON w.id=g.worktree WHERE g.worktree=(SELECT worktree FROM selection) OR g.branch=(SELECT branch FROM selection)", [], |r| Ok((r.get(0)?,r.get(1)?,r.get(2)?))).map_err(err)?;
        let run: String = db
            .query_row(
                "SELECT coalesce((SELECT artifact FROM scip_run LIMIT 1),'')",
                [],
                |r| r.get(0),
            )
            .map_err(err)?;
        let files = store::reads::files(&db)?;
        let freshness = precise::binding::select(&db, &files)?;
        let change_token = format!("{stamp}:{run}:{freshness}");
        let project = db
            .query_row("SELECT token FROM project", [], |r| r.get(0))
            .map_err(err)?;
        let id = format!("{project}:{worktree}:{generation}:{change_token}");
        Ok(Self {
            id,
            change_token,
            project,
            worktree,
            files,
            db,
        })
    }
    pub(super) fn precise_prefix(&self) -> Result<String, String> {
        let run: String = self
            .db
            .query_row(
                "SELECT coalesce((SELECT artifact FROM scip_run LIMIT 1),'')",
                [],
                |r| r.get(0),
            )
            .map_err(err)?;
        Ok(format!("{}:precise:{}:{run}:", self.project, self.worktree))
    }
    // Body bytes belong to this generation, including edits no longer on disk.
    pub fn body(&self, path: &str) -> Result<Option<Vec<u8>>, String> {
        use rusqlite::OptionalExtension;
        self.db.query_row("SELECT f.body FROM manifest_files m JOIN facts f ON f.id=m.fact WHERE m.manifest=(SELECT manifest FROM selection) AND m.path=?1", [path], |r| r.get(0)).optional().map(|v| v.flatten()).map_err(err)
    }
    pub fn ensure(&self, expected: &str) -> Result<(), String> {
        if self.id == expected {
            Ok(())
        } else {
            Err("code snapshot expired".into())
        }
    }
}
fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}

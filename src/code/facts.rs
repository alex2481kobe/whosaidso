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
    pub source_root: Option<std::path::PathBuf>,
    pub(crate) db: Connection,
}
impl Snapshot {
    pub fn extractor_version(&self) -> &str {
        self.change_token.split(':').next().unwrap_or_default()
    }
    pub fn has_precise(root: &Path) -> Result<bool, String> {
        let db = store::open(root)?;
        precise::storage::schema(&db)?;
        Ok(precise::binding::language(&db)?.is_some())
    }
    pub fn strict(&mut self) -> Result<Option<String>, String> {
        let language = precise::binding::strict(&self.db, &self.files)?;
        if language.is_some() {
            self.id.push_str(":strict");
            self.change_token.push_str(":strict");
        }
        Ok(language)
    }
    pub fn current(root: &Path) -> Result<Self, String> {
        let (db, _, _) = store::indexed(root)?;
        Self::from_db(db, Some(root))
    }
    pub fn sources_outside(root: &Path, include: impl Fn(&str) -> bool) -> Result<bool, String> {
        let listing =
            crate::git::run_bytes(root, &["ls-files", "-co", "--exclude-standard", "-z"])?;
        Ok(listing
            .split(|b| *b == 0)
            .filter_map(|p| std::str::from_utf8(p).ok())
            .any(|p| !include(p) && super::parse::supported(p)))
    }
    pub fn current_in(root: &Path, include: impl Fn(&str) -> bool) -> Result<Self, String> {
        let (db, _, _) = store::indexed_in(root, include)?;
        Self::from_db(db, Some(root))
    }
    pub fn published(root: &Path) -> Result<Self, String> {
        let db = store::open(root)?;
        Self::from_db(db, Some(root))
    }
    pub fn branch(root: &Path, branch: &str) -> Result<Self, String> {
        Self::from_db(store::branch::open_snapshot(root, branch)?, None)
    }
    pub fn branch_in(
        root: &Path,
        branch: &str,
        include: impl Fn(&str) -> bool,
    ) -> Result<Self, String> {
        Self::from_db(
            store::branch::open_snapshot_in(root, branch, include)?,
            None,
        )
    }
    fn from_db(db: Connection, root: Option<&Path>) -> Result<Self, String> {
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
        let freshness = precise::binding::select(&db)?;
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
            source_root: root.map(Path::to_path_buf),
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

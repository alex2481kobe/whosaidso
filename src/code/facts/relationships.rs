// Imported relationships are visible only in the selected worktree's fresh precise run.
use super::{Snapshot, err};
pub struct Relationship {
    pub source: String,
    pub target: String,
    pub is_implementation: bool,
}
impl Snapshot {
    pub(super) fn all_relationships(&self) -> Result<Vec<Relationship>, String> {
        let prefix = self.precise_prefix()?;
        let mut stmt = self.db.prepare("SELECT r.source,r.target,r.is_implementation FROM scip_relationships r WHERE (SELECT status='fresh' FROM precise_freshness) AND (r.path='' OR EXISTS (SELECT 1 FROM precise_documents d WHERE d.path=r.path AND d.status='fresh')) ORDER BY r.path,r.source,r.target").map_err(err)?;
        stmt.query_map([], |r| {
            Ok(Relationship {
                source: format!("{prefix}{}", r.get::<_, String>(0)?),
                target: format!("{prefix}{}", r.get::<_, String>(1)?),
                is_implementation: r.get(2)?,
            })
        })
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)
    }
}

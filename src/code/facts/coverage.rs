// Per-file extraction and precise freshness are observable, including unknown coverage.
use super::{Snapshot, err};
pub struct Coverage {
    pub path: String,
    pub syntax: String,
    pub precise: String,
}
impl Snapshot {
    pub fn coverage(&self) -> Result<Vec<Coverage>, String> {
        let mut stmt=self.db.prepare("SELECT m.path,coalesce(c.status,'not indexed'),CASE WHEN d.path IS NULL THEN 'not indexed' WHEN d.blob=m.blob THEN 'fresh' ELSE 'stale' END FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN scip_docs d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection) ORDER BY m.path").map_err(err)?;
        stmt.query_map([], |r| {
            Ok(Coverage {
                path: r.get(0)?,
                syntax: r.get(1)?,
                precise: r.get(2)?,
            })
        })
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)
    }
}

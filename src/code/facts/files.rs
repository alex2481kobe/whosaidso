use super::{Snapshot, err};
use rusqlite::params;

pub struct File {
    pub path: String,
    pub syntax: String,
    pub precise: String,
    pub definitions: usize,
}
pub struct Page {
    pub rows: Vec<File>,
    pub total: usize,
    pub freshness: &'static str,
}
const FILES: &str = "FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection)";
const VISIBLE: &str = "coalesce(c.status,'not indexed')<>'not indexed' OR d.status='fresh'";
impl Snapshot {
    // Count only the visible files' definitions; no symbol or reference rows are hydrated.
    pub fn file_page(&self, offset: usize, limit: usize) -> Result<Page, String> {
        let limit = i64::try_from(limit).map_err(|_| "file limit is too large")?;
        let offset = i64::try_from(offset).map_err(|_| "file offset is too large")?;
        let total: i64 = self
            .db
            .query_row(
                &format!("SELECT count(*) {FILES} AND ({VISIBLE})"),
                [],
                |r| r.get(0),
            )
            .map_err(err)?;
        let mut stmt = self.db.prepare(&format!("SELECT m.path,coalesce(c.status,'not indexed'),coalesce(d.status,'not indexed'),CASE WHEN d.status='fresh' THEN (SELECT count(*) FROM scip_occ o WHERE o.path=m.path AND o.is_def=1) ELSE (SELECT count(*) FROM syntax_defs s WHERE s.fact=m.fact) END {FILES} AND ({VISIBLE}) ORDER BY m.path LIMIT ?1 OFFSET ?2")).map_err(err)?;
        let rows = stmt
            .query_map(params![limit, offset], |r| {
                Ok(File {
                    path: r.get(0)?,
                    syntax: r.get(1)?,
                    precise: r.get(2)?,
                    definitions: r.get::<_, i64>(3)? as usize,
                })
            })
            .map_err(err)?
            .collect::<Result<_, _>>()
            .map_err(err)?;
        Ok(Page {
            rows,
            total: total as usize,
            freshness: self.freshness()?,
        })
    }
    pub fn freshness(&self) -> Result<&'static str, String> {
        // Unsupported files cannot be gaps. Start at coverage facts and precise
        // documents rather than revisiting every manifest path for this summary.
        let stale: bool = self.db.query_row("SELECT EXISTS(SELECT 1 FROM precise_documents d WHERE d.status='stale' AND EXISTS(SELECT 1 FROM manifest_files m WHERE m.manifest=(SELECT manifest FROM selection) AND m.path=d.path))",[],|r| r.get(0)).map_err(err)?;
        if stale {
            return Ok("stale");
        }
        let unknown: bool = self.db.query_row("SELECT EXISTS(SELECT 1 FROM precise_documents d WHERE d.status='unknown' AND EXISTS(SELECT 1 FROM manifest_files m WHERE m.manifest=(SELECT manifest FROM selection) AND m.path=d.path)) OR EXISTS(SELECT 1 FROM syntax_coverage c CROSS JOIN manifest_files m INDEXED BY manifest_fact ON m.fact=c.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE c.status NOT IN ('indexed','not indexed') AND m.manifest=(SELECT manifest FROM selection) AND coalesce(d.status,'not indexed')<>'fresh')",[],|r| r.get(0)).map_err(err)?;
        Ok(if unknown { "unknown" } else { "fresh" })
    }
}

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
}
const FILES: &str = "FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection) AND (coalesce(c.status,'not indexed')<>'not indexed' OR d.status='fresh')";
impl Snapshot {
    // Count only the visible files' definitions; no symbol or reference rows are hydrated.
    pub fn file_page(&self, offset: usize, limit: usize) -> Result<Page, String> {
        let limit = i64::try_from(limit).map_err(|_| "file limit is too large")?;
        let offset = i64::try_from(offset).map_err(|_| "file offset is too large")?;
        let total: i64 = self
            .db
            .query_row(&format!("SELECT count(*) {FILES}"), [], |r| r.get(0))
            .map_err(err)?;
        let mut stmt = self.db.prepare(&format!("SELECT m.path,coalesce(c.status,'not indexed'),coalesce(d.status,'not indexed'),CASE WHEN d.status='fresh' THEN (SELECT count(*) FROM scip_occ o WHERE o.path=m.path AND o.is_def=1) ELSE (SELECT count(*) FROM syntax_defs s WHERE s.fact=m.fact) END {FILES} ORDER BY m.path LIMIT ?1 OFFSET ?2")).map_err(err)?;
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
        })
    }
    pub fn freshness(&self) -> Result<&'static str, String> {
        let (stale,unknown):(bool,bool) = self.db.query_row("SELECT coalesce(max(d.status='stale'),0),coalesce(max(d.status='unknown' OR (coalesce(c.status,'not indexed') NOT IN ('indexed','not indexed') AND coalesce(d.status,'not indexed')<>'fresh')),0) FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection)",[],|r| Ok((r.get(0)?,r.get(1)?))).map_err(err)?;
        Ok(if stale {
            "stale"
        } else if unknown {
            "unknown"
        } else {
            "fresh"
        })
    }
}

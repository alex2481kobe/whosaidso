// Per-file extraction and precise freshness are observable, including unknown coverage.
use super::{Snapshot, err};
pub struct Coverage {
    pub path: String,
    pub syntax: String,
    pub precise: String,
}
impl Snapshot {
    pub fn coverage(&self) -> Result<Vec<Coverage>, String> { self.read_coverage(None) }
    pub fn coverage_in(&self, paths: &[String]) -> Result<Vec<Coverage>,String> { self.read_coverage(Some(paths)) }
    fn read_coverage(&self, paths: Option<&[String]>) -> Result<Vec<Coverage>, String> {
        let scope = paths.map(serde_json::to_string).transpose().map_err(err)?;
        let filter = if scope.is_some() { "AND m.path IN (SELECT value FROM json_each(?1))" } else { "" };
        let mut stmt=self.db.prepare(&format!("SELECT m.path,coalesce(c.status,'not indexed'),coalesce(d.status,'not indexed') FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection) {filter} ORDER BY m.path")).map_err(err)?;
        stmt.query_map(rusqlite::params_from_iter(scope.iter()), |r| {
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

impl Snapshot {
    pub fn blind_spots(&self, rows: &[super::reads::Row]) -> Result<Vec<String>, String> {
        use super::reads::Layer;
        let layers: Vec<_> = if rows.is_empty() {
            self.coverage()?
                .into_iter()
                .filter_map(|c| {
                    if c.precise == "fresh" {
                        Some((c.path, Layer::Precise))
                    } else if c.syntax == "indexed" {
                        Some((c.path, Layer::Syntactic))
                    } else {
                        None
                    }
                })
                .collect()
        } else {
            rows.iter().map(|r| (r.path.clone(), r.layer)).collect()
        };
        let mut gaps = std::collections::BTreeSet::new();
        for (path, layer) in layers {
            if let Some(language) = crate::code::languages::for_path(&path) {
                let (label, gap) = if layer == Layer::Precise {
                    (
                        "SCIP guidance, not locally reproduced",
                        language.precise_gap,
                    )
                } else {
                    ("syntactic", language.syntax_gap)
                };
                gaps.insert(format!("{} ({label}): {gap}", language.name));
            } else {
                gaps.insert(
                    "precise language coverage not probed; completeness unknown".to_string(),
                );
            }
        }
        Ok(gaps.into_iter().collect())
    }
    pub fn coverage_summary(&self) -> Result<(usize, usize, usize), String> {
        self.db.query_row("SELECT coalesce(sum(d.status='fresh'),0),coalesce(sum((d.path IS NULL OR d.status<>'fresh') AND c.status='indexed'),0),count(*) FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact LEFT JOIN precise_documents d ON d.path=m.path WHERE m.manifest=(SELECT manifest FROM selection)", [], |r| {
            let precise: i64 = r.get(0)?; let syntax: i64 = r.get(1)?; let total: i64 = r.get(2)?;
            Ok((precise as usize, syntax as usize, (total-precise-syntax) as usize))
        }).map_err(err)
    }
}

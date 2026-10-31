// Imported relationships are visible only in the selected worktree's fresh precise run.
use super::{Snapshot, edges::Direction, err};
use rusqlite::params;
pub struct Relationship {
    pub source: String,
    pub target: String,
    pub is_reference: bool,
    pub is_implementation: bool,
    pub is_type_definition: bool,
    pub is_definition: bool,
}
impl Snapshot {
    pub fn relationships(
        &self,
        symbol: &str,
        direction: Direction,
    ) -> Result<Vec<Relationship>, String> {
        let prefix = self.precise_prefix()?;
        let raw = symbol
            .strip_prefix(&prefix)
            .ok_or("symbol is not in this precise run")?;
        self.read_relationships(Some(raw), direction, &prefix)
    }
    pub(super) fn all_relationships(&self) -> Result<Vec<Relationship>, String> {
        self.read_relationships(None, Direction::Forward, &self.precise_prefix()?)
    }
    fn read_relationships(
        &self,
        symbol: Option<&str>,
        direction: Direction,
        prefix: &str,
    ) -> Result<Vec<Relationship>, String> {
        let column = match direction {
            Direction::Forward => "source",
            Direction::Reverse => "target",
        };
        let mut stmt = self.db.prepare(&format!("SELECT r.source,r.target,r.is_reference,r.is_implementation,r.is_type_definition,r.is_definition FROM scip_relationships r WHERE (?1 IS NULL OR r.{column}=?1) AND (SELECT status='fresh' FROM precise_freshness) AND (r.path='' OR EXISTS (SELECT 1 FROM precise_documents d WHERE d.path=r.path AND d.status='fresh')) ORDER BY r.path,r.source,r.target")).map_err(err)?;
        stmt.query_map(params![symbol], |r| {
            Ok(Relationship {
                source: format!("{prefix}{}", r.get::<_, String>(0)?),
                target: format!("{prefix}{}", r.get::<_, String>(1)?),
                is_reference: r.get(2)?,
                is_implementation: r.get(3)?,
                is_type_definition: r.get(4)?,
                is_definition: r.get(5)?,
            })
        })
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)
    }
}

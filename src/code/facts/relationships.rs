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
        self.read_relationships(Some(&[raw]), direction, &prefix)
    }
    pub(super) fn all_relationships(&self) -> Result<Vec<Relationship>, String> {
        self.read_relationships(None, Direction::Forward, &self.precise_prefix()?)
    }
    pub(super) fn relationships_from(&self, symbols: &[&str]) -> Result<Vec<Relationship>, String> {
        let prefix = self.precise_prefix()?;
        let raw = symbols
            .iter()
            .map(|s| {
                s.strip_prefix(&prefix)
                    .ok_or("symbol is not in this precise run")
            })
            .collect::<Result<Vec<_>, _>>()?;
        self.read_relationships(Some(&raw), Direction::Forward, &prefix)
    }
    fn read_relationships(
        &self,
        symbols: Option<&[&str]>,
        direction: Direction,
        prefix: &str,
    ) -> Result<Vec<Relationship>, String> {
        let column = match direction {
            Direction::Forward => "source",
            Direction::Reverse => "target",
        };
        let filter = if symbols.is_some() {
            format!("r.{column} IN (SELECT value FROM json_each(?1))")
        } else {
            "?1 IS NULL".into()
        };
        let parameter = symbols
            .map(serde_json::to_string)
            .transpose()
            .map_err(super::err)?;
        let order = if symbols.is_some_and(|s| s.len() > 1) {
            "r.source,r.path,r.target"
        } else {
            "r.path,r.source,r.target"
        };
        let mut stmt = self.db.prepare(&format!("SELECT r.source,r.target,r.is_reference,r.is_implementation,r.is_type_definition,r.is_definition FROM scip_relationships r WHERE {filter} AND (SELECT status='fresh' FROM precise_freshness) AND (r.path='' OR EXISTS (SELECT 1 FROM precise_documents d WHERE d.path=r.path AND d.status='fresh')) ORDER BY {order}")).map_err(err)?;
        stmt.query_map(params![parameter], |r| {
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

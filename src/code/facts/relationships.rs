// Imported semantic relationships remain scoped to fresh documents in the selected run.
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
        let run: String = self
            .db
            .query_row(
                "SELECT coalesce((SELECT artifact FROM scip_run LIMIT 1),'')",
                [],
                |r| r.get(0),
            )
            .map_err(err)?;
        let prefix = format!("{}:precise:{}:{run}:", self.project, self.worktree);
        let raw = symbol
            .strip_prefix(&prefix)
            .ok_or("symbol is not in this precise run")?;
        let column = match direction {
            Direction::Forward => "source",
            Direction::Reverse => "target",
        };
        let mut stmt=self.db.prepare(&format!("SELECT r.source,r.target,r.is_reference,r.is_implementation,r.is_type_definition,r.is_definition FROM scip_relationships r WHERE r.{column}=?1 AND (r.path='' OR EXISTS (SELECT 1 FROM scip_docs d JOIN files f ON f.path=d.path AND f.blob=d.blob WHERE d.path=r.path)) ORDER BY r.path,r.source,r.target")).map_err(err)?;
        stmt.query_map(params![raw], |r| {
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

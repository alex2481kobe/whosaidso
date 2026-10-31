// Hybrid rows retain their source, identity, range, and uncertainty.
use super::{Snapshot, err};
use crate::code::{
    contracts::{leaf, matches},
    precise,
};
use rusqlite::params;

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Layer {
    Precise,
    Syntactic,
}
pub struct Range {
    pub encoding: i64,
    pub line: i64,
    pub start: Option<i64>,
    pub end_line: i64,
    pub stop: Option<i64>,
}
pub struct Row {
    pub symbol: String,
    pub path: String,
    pub range: Range,
    pub name: String,
    pub qualified: String,
    pub kind: String,
    pub caller: String,
    pub container: Option<String>,
    pub layer: Layer,
    pub ambiguous: bool,
    pub documentation: Vec<String>,
    pub enclosing: Option<Range>,
}
pub struct Complete {
    pub rows: Vec<Row>,
    pub gaps: Vec<String>,
}
impl Snapshot {
    pub fn definitions(&self, value: &str) -> Result<Complete, String> {
        self.lookup(value, true)
    }
    pub fn references(&self, value: &str) -> Result<Complete, String> {
        self.lookup(value, false)
    }
    pub(super) fn all_references(&self) -> Result<Complete, String> {
        self.lookup("", false)
    }
    fn lookup(&self, value: &str, def: bool) -> Result<Complete, String> {
        let (fresh, precise) = precise::rows(&self.db, &self.files, value, def)?;
        let mut rows = Vec::new();
        let run: String = self
            .db
            .query_row(
                "SELECT coalesce((SELECT artifact FROM scip_run LIMIT 1),'')",
                [],
                |r| r.get(0),
            )
            .map_err(err)?;
        let id =
            |symbol: &str| format!("{}:precise:{}:{run}:{symbol}", self.project, self.worktree);
        for p in precise {
            rows.push(Row {
                symbol: id(&p.symbol),
                path: p.path,
                range: Range {
                    encoding: p.encoding,
                    line: p.line,
                    start: Some(p.start),
                    end_line: p.end_line,
                    stop: Some(p.stop),
                },
                name: p.name,
                qualified: p.qualified,
                kind: String::new(),
                caller: p.caller,
                container: p.container.as_deref().map(id),
                layer: Layer::Precise,
                ambiguous: false,
                documentation: p.documentation,
                enclosing: p.enclosing.map(|e| Range {
                    encoding: p.encoding,
                    line: e[0],
                    start: Some(e[1]),
                    end_line: e[2],
                    stop: Some(e[3]),
                }),
            });
        }
        let (table, name, kind, caller) = if def {
            ("syntax_defs", "name", "kind", "''")
        } else {
            ("syntax_calls", "callee", "''", "caller")
        };
        let filter = if value.is_empty() {
            String::new()
        } else {
            format!("AND d.{name}=?1")
        };
        let index = if def {
            "syntax_defs_name"
        } else {
            "syntax_calls_callee"
        };
        let sql = format!(
            "SELECT m.path,d.line,d.{name},{kind},{caller},d.qualified,d.start,d.end_line,d.stop,f.blob,f.extractor FROM {table} d INDEXED BY {index} CROSS JOIN manifest_files m INDEXED BY manifest_fact ON m.fact=d.fact JOIN facts f ON f.id=m.fact WHERE m.manifest=(SELECT manifest FROM selection) {filter} ORDER BY m.path,d.line"
        );
        let mut stmt = self.db.prepare(&sql).map_err(err)?;
        let map = |r: &rusqlite::Row<'_>| {
            let path: String = r.get(0)?;
            let line = r.get(1)?;
            let qualified: String = r.get(5)?;
            let blob: String = r.get(9)?;
            let extractor: String = r.get(10)?;
            Ok(Row {
                symbol: format!(
                    "{}:syntax:{blob}:{extractor}:{path}:{qualified}:{line}:{}",
                    self.project,
                    r.get::<_, i64>(6)?
                ),
                path,
                range: Range {
                    encoding: 1,
                    line,
                    start: r.get(6)?,
                    end_line: r.get(7)?,
                    stop: r.get(8)?,
                },
                name: r.get(2)?,
                qualified,
                kind: r.get(3)?,
                caller: r.get(4)?,
                container: None,
                layer: Layer::Syntactic,
                ambiguous: !def,
                documentation: Vec::new(),
                enclosing: None,
            })
        };
        let syntax = stmt
            .query_map(
                rusqlite::params_from_iter((!value.is_empty()).then(|| leaf(value))),
                map,
            )
            .map_err(err)?;
        for row in syntax {
            let mut row = row.map_err(err)?;
            if !fresh.contains(&row.path) && (value.is_empty() || matches(&row.qualified, value)) {
                row.container = self.container(&row.path, &row.range)?;
                rows.push(row);
            }
        }
        let mut gaps =
            vec!["syntactic references match names; they do not resolve types or imports".into()];
        let missing: i64 = self.db.query_row("SELECT count(*) FROM manifest_files m LEFT JOIN syntax_coverage c ON c.fact=m.fact WHERE m.manifest=(SELECT manifest FROM selection) AND (c.status IS NULL OR c.status<>'indexed')", [], |r| r.get(0)).map_err(err)?;
        if missing > 0 {
            gaps.push(format!("{missing} files have no syntax extraction"));
        }
        let stale: i64 = self.db.query_row("SELECT count(*) FROM scip_docs d WHERE NOT EXISTS (SELECT 1 FROM files f WHERE f.path=d.path AND f.blob=d.blob)",[],|r|r.get(0)).map_err(err)?;
        if stale > 0 {
            gaps.push(format!(
                "{stale} precise documents are stale; syntax fills the remaining files"
            ));
        }
        Ok(Complete { rows, gaps })
    }
    fn container(&self, path: &str, range: &Range) -> Result<Option<String>, String> {
        use rusqlite::OptionalExtension;
        self.db.query_row("SELECT f.blob,f.extractor,d.qualified,d.line,d.start FROM manifest_files m JOIN facts f ON f.id=m.fact JOIN syntax_defs d ON d.fact=m.fact WHERE m.manifest=(SELECT manifest FROM selection) AND m.path=?1 AND (d.line<?2 OR (d.line=?2 AND d.start<=?4)) AND (d.end_line>?3 OR (d.end_line=?3 AND d.stop>=?5)) AND (d.line<?2 OR d.start<?4 OR d.end_line>?3 OR d.stop>?5) ORDER BY d.end_line-d.line,d.stop-d.start LIMIT 1", params![path,range.line,range.end_line,range.start,range.stop], |r| Ok(format!("{}:syntax:{}:{}:{path}:{}:{}:{}",self.project,r.get::<_,String>(0)?,r.get::<_,String>(1)?,r.get::<_,String>(2)?,r.get::<_,i64>(3)?,r.get::<_,i64>(4)?))).optional().map_err(err)
    }
}

use rusqlite::{Connection, params_from_iter, types::Value};

pub(super) struct Hit {
    pub kind: String,
    pub key: String,
    pub title: String,
    pub matched: usize,
    pub edits: usize,
    pub bm: f64,
}
pub(super) struct Pool {
    values: Vec<Value>,
    coverage: String,
    edits: String,
    pub terms: Vec<String>,
}
impl Pool {
    pub fn new(
        words: &[String],
        corrected: &[(String, usize)],
        kind: Option<&str>,
        cap: usize,
    ) -> Self {
        // Reject weak partial matches independently of corpus-dependent BM25 scores.
        let mut values = vec![
            Value::Null,
            Value::Integer(cap as i64),
            Value::Integer(words.len().div_ceil(2) as i64),
            kind.map_or(Value::Null, |k| Value::Text(k.to_owned())),
            Value::Null,
        ];
        let mut coverage = Vec::new();
        let mut edits = Vec::new();
        let mut terms = Vec::new();
        for (original, (fixed, cost)) in words.iter().zip(corrected) {
            let term = format!("\"{original}\"*");
            values.push(Value::Text(format!("{{title body}} : ({term})")));
            let exact = format!(
                "docs.id IN (SELECT rowid FROM docs_fts WHERE docs_fts MATCH ?{})",
                values.len()
            );
            if fixed == original {
                coverage.push(format!("({exact})"));
                terms.push(term);
            } else {
                let fix = format!("\"{fixed}\"*");
                values.push(Value::Text(format!("{{title body}} : ({fix})")));
                let fuzzy = format!(
                    "docs.id IN (SELECT rowid FROM docs_fts WHERE docs_fts MATCH ?{})",
                    values.len()
                );
                coverage.push(format!("({exact} OR {fuzzy})"));
                edits.push(format!(
                    "CASE WHEN {exact} THEN 0 WHEN {fuzzy} THEN {cost} ELSE 0 END"
                ));
                terms.push(format!("({term} OR {fix})"));
            }
        }
        Self {
            values,
            coverage: coverage.join("+"),
            edits: if edits.is_empty() {
                "0".into()
            } else {
                edits.join("+")
            },
            terms,
        }
    }
    fn source(&self) -> String {
        format!(
            "FROM docs_fts JOIN docs ON docs.id=docs_fts.rowid WHERE docs_fts MATCH ?1 AND (?4 IS NULL OR docs.kind=?4) AND ({})>=?3",
            self.coverage
        )
    }
    fn bind(&mut self, terms: &str) {
        self.values[0] = Value::Text(format!("{{title body}} : ({terms})"));
    }
    pub fn fetch(
        &mut self,
        db: &Connection,
        terms: &str,
        exact_title: Option<&str>,
    ) -> Result<Vec<Hit>, String> {
        self.bind(terms);
        self.values[4] = exact_title.map_or(Value::Null, |title| Value::Text(title.to_owned()));
        let sql = format!(
            "SELECT docs.kind,docs.key,docs.title,({}) AS matched,({}) AS edits,bm25(docs_fts,0,1,1) AS bm {} AND (?5 IS NULL OR docs.title=?5 COLLATE NOCASE) ORDER BY bm,docs.id LIMIT CASE WHEN ?5 IS NULL THEN ?2 ELSE -1 END",
            self.coverage,
            self.edits,
            self.source()
        );
        let mut stmt = db.prepare(&sql).map_err(|e| e.to_string())?;
        let mut rows = stmt
            .query_map(params_from_iter(&self.values), |r| {
                Ok(Hit {
                    kind: r.get(0)?,
                    key: r.get(1)?,
                    title: r.get(2)?,
                    matched: r.get::<_, i64>(3)? as usize,
                    edits: r.get::<_, i64>(4)? as usize,
                    bm: r.get(5)?,
                })
            })
            .map_err(|e| e.to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?;
        let best = rows.iter().map(|h| -h.bm).fold(f64::MIN_POSITIVE, f64::max);
        for hit in &mut rows {
            hit.bm = -hit.bm / best;
        }
        Ok(rows)
    }
    pub fn total(&mut self, db: &Connection) -> Result<i64, String> {
        self.bind(&self.terms.join(" OR "));
        db.query_row(
            &format!("SELECT count(*) {}", self.source()),
            params_from_iter(&self.values),
            |r| r.get(0),
        )
        .map_err(|e| e.to_string())
    }
}

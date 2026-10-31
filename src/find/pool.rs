use rusqlite::{Connection, params_from_iter, types::Value};

pub(crate) struct Hit {
    pub source: String,
    pub method: Option<&'static str>,
    pub kind: String,
    pub key: String,
    pub title: String,
    pub matched: usize,
    pub edits: usize,
    pub bm: f64,
    pub priority: i64,
    pub text_bytes: i64,
}
pub(super) struct Pool {
    values: Vec<Value>,
    coverage: String,
    edits: String,
    pub terms: Vec<String>,
    kinds: Vec<String>,
}
impl Pool {
    pub fn new(
        words: &[String],
        corrected: &[(String, usize)],
        kinds: &[&str],
        cap: usize,
    ) -> Self {
        // Reject weak partial matches independently of corpus-dependent BM25 scores.
        let mut values = vec![
            Value::Null,
            Value::Integer(cap as i64),
            Value::Integer(words.len().div_ceil(2).min(2) as i64),
            if kinds.is_empty() {
                Value::Null
            } else {
                Value::Text(format!("|{}|", kinds.join("|")))
            },
            Value::Null,
        ];
        let mut coverage = Vec::new();
        let mut edits = Vec::new();
        let mut terms = Vec::new();
        for (original, (fixed, cost)) in words.iter().zip(corrected) {
            let term = expression(original);
            values.push(Value::Text(format!("{{title body}} : ({term})")));
            let exact = format!(
                "docs.id IN (SELECT rowid FROM docs_fts WHERE docs_fts MATCH ?{})",
                values.len()
            );
            if fixed == original {
                coverage.push(format!("({exact})"));
                terms.push(term);
            } else {
                let fix = expression(fixed);
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
            kinds: kinds.iter().map(|s| s.to_string()).collect(),
            coverage: coverage.join("+"),
            edits: if edits.is_empty() {
                "0".into()
            } else {
                edits.join("+")
            },
            terms,
        }
    }
    fn source(&self, require_floor: bool) -> String {
        let floor = if require_floor {
            format!(" AND ({})>=?3", self.coverage)
        } else {
            String::new()
        };
        format!(
            "FROM docs_fts JOIN docs ON docs.id=docs_fts.rowid WHERE docs_fts MATCH ?1 AND (?4 IS NULL OR instr(?4,'|'||docs.kind||'|')>0){floor}"
        )
    }
    fn bind(&mut self, terms: &str) {
        let kind = if self.kinds.is_empty() {
            String::new()
        } else {
            format!(
                " AND kind : ({})",
                self.kinds
                    .iter()
                    .map(|k| format!("\"{}\"", k.replace('"', "\"\"")))
                    .collect::<Vec<_>>()
                    .join(" OR ")
            )
        };
        self.values[0] = Value::Text(format!("{{title body}} : ({terms}){kind}"));
    }
    pub fn fetch(
        &mut self,
        db: &Connection,
        terms: &str,
        exact_title: Option<&str>,
    ) -> Result<Vec<Hit>, String> {
        self.bind(terms);
        self.values[4] = exact_title.map_or(Value::Null, |title| Value::Text(title.to_owned()));
        let all = terms == self.terms.join(" AND ") && self.edits == "0";
        let matched = if all {
            self.terms.len().to_string()
        } else {
            self.coverage.clone()
        };
        let sql = if exact_title.is_some() {
            format!(
                "SELECT docs.kind,docs.key,docs.title,({}) AS matched,({}) AS edits,bm25(docs_fts,0,1,1) AS bm,docs.source,docs.priority,docs.text_bytes {} AND (?5 IS NULL OR docs.title=?5 COLLATE NOCASE) ORDER BY matched DESC,edits,bm,docs.id LIMIT ?2",
                matched,
                self.edits,
                self.source(!all)
            )
        } else {
            let coverage = matched.replace("docs.id", "docs_fts.rowid");
            let edits = self.edits.replace("docs.id", "docs_fts.rowid");
            let floor = if all {
                String::new()
            } else {
                "WHERE matched>=?3".to_owned()
            };
            let features = crate::index::features::projection("m");
            let size = crate::index::features::SIZE_BUCKET;
            let order = format!("priority DESC,matched DESC,edits,text_bytes/{size},bm,id,key");
            let selected_order = order
                .split(',')
                .map(|part| format!("r.{part}"))
                .collect::<Vec<_>>()
                .join(",");
            // Keep a source's best file row and best symbol before applying the
            // candidate cap, then read only those compact winners' payloads.
            format!(
                "WITH postings AS MATERIALIZED (SELECT docs_fts.rowid AS id,m.key,m.source,{features},({coverage}) AS matched,({edits}) AS edits,bm25(docs_fts,0,1,1) AS bm FROM docs_fts CROSS JOIN members m INDEXED BY member_lookup WHERE docs_fts MATCH ?1 AND m.worktree=(SELECT worktree FROM selection) AND m.payload=docs_fts.rowid), ranked AS MATERIALIZED (SELECT *,row_number() OVER (PARTITION BY source,(key LIKE 'symbol:%') ORDER BY {order}) AS position FROM postings {floor}), selected AS MATERIALIZED (SELECT * FROM ranked WHERE position=1 ORDER BY {order} LIMIT ?2) SELECT p.kind,r.key,CASE WHEN p.kind='file' THEN substr(r.key,6) ELSE p.title END,r.matched,r.edits,r.bm,r.source,r.priority,r.text_bytes FROM selected r JOIN payloads p ON p.id=r.id ORDER BY {selected_order}"
            )
        };
        let mut stmt = db.prepare(&sql).map_err(|e| e.to_string())?;
        let count = stmt.parameter_count();
        let mut rows = stmt
            .query_map(params_from_iter(&self.values[..count]), |r| {
                Ok(Hit {
                    source: r.get(6)?,
                    method: None,
                    kind: r.get(0)?,
                    key: r.get(1)?,
                    title: r.get(2)?,
                    matched: r.get::<_, i64>(3)? as usize,
                    edits: r.get::<_, i64>(4)? as usize,
                    bm: r.get(5)?,
                    priority: r.get(7)?,
                    text_bytes: r.get(8)?,
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
    pub fn total(&mut self, db: &Connection, terms: &str) -> Result<i64, String> {
        self.bind(terms);
        // Count membership keys without reading the much larger payload rows.
        let coverage = if terms == self.terms.join(" AND ") {
            self.terms.len().to_string()
        } else {
            self.coverage.replace("docs.id", "docs_fts.rowid")
        };
        let sql = format!(
            "SELECT count(DISTINCT CASE WHEN source LIKE 'file:%' THEN source ELSE key END) FROM members INDEXED BY member_lookup WHERE worktree=(SELECT worktree FROM selection) AND payload IN (SELECT rowid FROM docs_fts WHERE docs_fts MATCH ?1 AND ({coverage})>=?3)"
        );
        {
            let mut stmt = db.prepare(&sql).map_err(|e| e.to_string())?;
            let count = stmt.parameter_count();
            stmt.query_row(params_from_iter(&self.values[..count]), |r| r.get(0))
        }
        .map_err(|e| e.to_string())
    }
}

fn expression(word: &str) -> String {
    let stem = crate::index::terms::stem(word);
    if stem == word {
        format!("\"{word}\"*")
    } else {
        format!("(\"{word}\"* OR \"{stem}\"*)")
    }
}

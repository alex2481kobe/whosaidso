// Import reads use the same selected fact generation as symbol reads.
use super::Snapshot;
use std::path::Path;
impl Snapshot {
    pub fn dependencies(&self, path: &str) -> Result<Vec<String>, String> {
        super::super::store::reads::dependencies(&self.db, path)
    }
    pub fn includers(&self, path: &str) -> Result<Vec<String>, String> {
        let name = Path::new(path)
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("");
        Ok(super::super::store::reads::includers(&self.db)?
            .into_iter()
            .filter(|(_, _, target)| {
                !name.is_empty()
                    && (target == name
                        || target.strip_suffix(name).is_some_and(|p| p.ends_with('/')))
            })
            .map(|(path, line, _)| format!("included by {path}:{line}  [syntactic, by name]"))
            .collect())
    }
}

// Import declarations bind names; their later uses provide the dependency evidence.
impl Snapshot {
    pub(super) fn symbol_uses(
        &self,
        mut rows: Vec<super::reads::Row>,
    ) -> Result<Vec<super::reads::Row>, String> {
        use super::reads::Layer;
        use std::collections::{BTreeMap, HashMap};
        use tree_sitter::{CaptureQuantifier, Parser, Query, QueryCursor, StreamingIterator};
        rows.retain(|r| r.kind != "namespace");
        let mut imports = BTreeMap::new();
        let mut queries = HashMap::new();
        let mut parser = Parser::new();
        for row in rows.iter().filter(|r| r.layer == Layer::Precise) {
            if imports.contains_key(&row.path) {
                continue;
            }
            let mut ranges = Vec::new();
            if let Some(language) = super::super::languages::for_path(&row.path) {
                let grammar = (language.grammar)();
                parser.set_language(&grammar).map_err(super::err)?;
                if !queries.contains_key(language.name) {
                    let mut query = Query::new(&grammar, language.query).map_err(super::err)?;
                    let include = query
                        .capture_names()
                        .iter()
                        .position(|&name| name == "include");
                    for pattern in 0..query.pattern_count() {
                        if include.is_none_or(|i| {
                            query.capture_quantifiers(pattern)[i] == CaptureQuantifier::Zero
                        }) {
                            query.disable_pattern(pattern);
                        }
                    }
                    queries.insert(language.name, query);
                }
                if let Some(bytes) = self.body(&row.path)? {
                    let tree = parser
                        .parse(&bytes, None)
                        .ok_or("failed to read import declarations")?;
                    let query = &queries[language.name];
                    let lines: Vec<_> = bytes.split(|&b| b == b'\n').collect();
                    let point = |p: tree_sitter::Point| -> Result<_, String> {
                        let prefix =
                            std::str::from_utf8(&lines[p.row][..p.column]).map_err(super::err)?;
                        let column = match row.range.encoding {
                            2 => prefix.encode_utf16().count(),
                            3 => prefix.chars().count(),
                            _ => p.column,
                        };
                        Ok((p.row as i64 + 1, column as i64))
                    };
                    let mut cursor = QueryCursor::new();
                    let mut captures = cursor.matches(query, tree.root_node(), bytes.as_slice());
                    while let Some(m) = captures.next() {
                        for c in m.captures() {
                            if query.capture_names()[c.index as usize] == "include" {
                                ranges.push((
                                    point(c.node.start_position())?,
                                    point(c.node.end_position())?,
                                ));
                            }
                        }
                    }
                }
            }
            imports.insert(row.path.clone(), ranges);
        }
        rows.retain(|r| {
            !imports.get(&r.path).is_some_and(|ranges| {
                ranges.iter().any(|&(start, end)| {
                    start <= (r.range.line, r.range.start.unwrap_or(-1))
                        && (r.range.end_line, r.range.stop.unwrap_or(i64::MAX)) <= end
                })
            })
        });
        Ok(rows)
    }
}

impl Snapshot {
    pub(super) fn rust_aliases(
        &self,
        rows: &[super::reads::Row],
    ) -> Result<std::collections::HashMap<String, std::collections::HashSet<String>>, String> {
        use std::collections::{BTreeSet, HashMap};
        let paths: BTreeSet<_> = rows
            .iter()
            .filter(|r| r.layer == super::reads::Layer::Syntactic && r.path.ends_with(".rs"))
            .map(|r| r.path.as_str())
            .collect();
        let mut aliases = HashMap::new();
        if paths.is_empty() {
            return Ok(aliases);
        }
        for path in paths {
            if !self.dependencies(path)?.iter().any(|p| p.contains("as")) {
                continue;
            }
            let bytes = self
                .body(path)?
                .ok_or("missing Rust source for alias resolution")?;
            for alias in super::super::parse::names::rust_aliases(&bytes)? {
                aliases
                    .entry(path.to_owned())
                    .or_insert_with(std::collections::HashSet::new)
                    .insert(alias);
            }
        }
        Ok(aliases)
    }
}

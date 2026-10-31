// A hybrid graph retains exact precise identities and every syntax-name candidate.
use super::{
    Snapshot,
    reads::{Layer, Row},
    relationships::Relationship,
};
use std::collections::{BTreeSet, HashMap, HashSet};

#[derive(Clone, Copy)]
pub enum Direction {
    Forward,
    Reverse,
}
#[derive(Clone)]
pub struct Edge {
    pub source: Option<String>,
    pub targets: Vec<String>,
    pub occurrence: Row,
    pub unresolved: bool,
}
pub struct Edges {
    pub rows: Vec<Edge>,
    pub gaps: Vec<String>,
}
pub struct Graph {
    pub definitions: Vec<Row>,
    pub edges: Vec<Edge>,
    pub relationships: Vec<Relationship>,
    pub gaps: Vec<String>,
}
impl Snapshot {
    pub fn graph(&self) -> Result<Graph, String> {
        let definitions = self.definitions("")?.rows;
        let refs = self.all_references()?;
        let aliases = self.rust_aliases(&refs.rows)?;
        let unvalidated = self.unvalidated_targets(&refs.rows)?;
        Ok(assemble(
            definitions,
            self.symbol_uses(refs.rows)?,
            self.all_relationships()?,
            refs.gaps,
            aliases,
            unvalidated,
        ))
    }
    // Only selected callers and requested inbound names supply references. Name candidates
    // still resolve across the snapshot so narrowing cannot turn ambiguity into certainty.
    pub fn graph_in(&self, paths: &[String], incoming: &[String]) -> Result<Graph, String> {
        let mut refs = self.references_in(paths)?;
        for name in incoming {
            refs.rows.extend(self.candidates(name, false)?.rows);
        }
        refs.rows = self.symbol_uses(refs.rows)?;
        let mut seen = HashSet::new();
        refs.rows.retain(|r| {
            seen.insert((
                r.path.clone(),
                r.range.line,
                r.range.start,
                r.symbol.clone(),
            ))
        });
        let mut source_paths: Vec<_> = paths.to_vec();
        source_paths.extend(refs.rows.iter().map(|r| r.path.clone()));
        source_paths.sort();
        source_paths.dedup();
        let names: HashSet<_> = refs.rows.iter().map(|r| r.name.as_str()).collect();
        let encoded = serde_json::to_string(&names).map_err(super::err)?;
        // Resolve all candidate paths together: one lookup per name repeatedly scans SCIP.
        let mut stmt = self.db.prepare("SELECT m.path FROM json_each(?1) n JOIN syntax_defs d INDEXED BY syntax_defs_name ON d.name=n.value JOIN manifest_files m INDEXED BY manifest_fact ON m.fact=d.fact WHERE m.manifest=(SELECT manifest FROM selection) UNION SELECT o.path FROM json_each(?1) n CROSS JOIN scip_syms s INDEXED BY scip_syms_name ON s.name=n.value CROSS JOIN scip_occ o INDEXED BY scip_occ_symbol ON o.symbol=s.symbol WHERE o.is_def=1").map_err(super::err)?;
        let mut candidate_paths = source_paths.clone();
        candidate_paths.extend(
            stmt.query_map([encoded], |r| r.get::<_, String>(0))
                .map_err(super::err)?
                .collect::<Result<Vec<_>, _>>()
                .map_err(super::err)?,
        );
        candidate_paths.sort();
        candidate_paths.dedup();
        let mut definitions = self.definitions_in(&candidate_paths)?.rows;
        definitions.sort_by_key(|r| source_paths.binary_search(&r.path).is_err());
        definitions.retain(|r| {
            source_paths.binary_search(&r.path).is_ok() || names.contains(r.name.as_str())
        });
        let mut seen = HashSet::new();
        definitions.retain(|r| {
            seen.insert((
                r.symbol.clone(),
                r.path.clone(),
                r.range.line,
                r.range.start,
            ))
        });
        let sources: BTreeSet<_> = definitions
            .iter()
            .filter(|r| r.layer == Layer::Precise && source_paths.binary_search(&r.path).is_ok())
            .map(|r| r.symbol.as_str())
            .collect();
        let relationships = self.relationships_from(&sources.into_iter().collect::<Vec<_>>())?;
        let aliases = self.rust_aliases(&refs.rows)?;
        let unvalidated = self.unvalidated_targets(&refs.rows)?;
        Ok(assemble(
            definitions,
            refs.rows,
            relationships,
            refs.gaps,
            aliases,
            unvalidated,
        ))
    }
    // Preserve uncertainty when a precise reference's definition fell back to syntax.
    fn unvalidated_targets(&self, references: &[Row]) -> Result<HashSet<String>, String> {
        let incomplete: bool = self
            .db
            .query_row(
                "SELECT EXISTS(SELECT 1 FROM precise_documents WHERE status<>'fresh')",
                [],
                |r| r.get(0),
            )
            .map_err(super::err)?;
        if !incomplete {
            return Ok(HashSet::new());
        }
        let prefix = self.precise_prefix()?;
        let symbols: HashSet<_> = references
            .iter()
            .filter(|r| r.layer == Layer::Precise)
            .filter_map(|r| r.symbol.strip_prefix(&prefix))
            .collect();
        if symbols.is_empty() {
            return Ok(HashSet::new());
        }
        let mut stmt = self.db.prepare("SELECT DISTINCT o.symbol FROM json_each(?1) n CROSS JOIN scip_occ o INDEXED BY scip_occ_symbol ON o.symbol=n.value JOIN precise_documents d ON d.path=o.path WHERE o.is_def=1 AND d.status<>'fresh'").map_err(super::err)?;
        stmt.query_map(
            [serde_json::to_string(&symbols).map_err(super::err)?],
            |r| r.get::<_, String>(0),
        )
        .map_err(super::err)?
        .map(|r| r.map(|s| format!("{prefix}{s}")).map_err(super::err))
        .collect()
    }
    pub fn edges(&self, symbol: &str, direction: Direction) -> Result<Edges, String> {
        let graph = self.graph()?;
        if !graph.definitions.iter().any(|r| r.symbol == symbol) {
            return Err("symbol is not in this snapshot".into());
        }
        let rows = graph
            .edges
            .into_iter()
            .filter(|e| match direction {
                Direction::Forward => e.source.as_deref() == Some(symbol),
                Direction::Reverse => e.targets.iter().any(|t| t == symbol),
            })
            .collect();
        Ok(Edges {
            rows,
            gaps: graph.gaps,
        })
    }
}

fn assemble(
    definitions: Vec<Row>,
    references: Vec<Row>,
    relationships: Vec<Relationship>,
    gaps: Vec<String>,
    aliases: HashMap<String, HashSet<String>>,
    unvalidated: HashSet<String>,
) -> Graph {
    let mut names: HashMap<&str, Vec<&Row>> = HashMap::new();
    for d in &definitions {
        names.entry(&d.name).or_default().push(d);
    }
    let mut rust: HashMap<Vec<String>, Vec<&Row>> = HashMap::new();
    for d in &definitions {
        if let Some(mut path) = rust_module(&d.path) {
            path.extend(d.qualified.split("::").map(str::to_owned));
            for at in 0..path.len().saturating_sub(1) {
                rust.entry(path[at..].to_vec()).or_default().push(d);
            }
        }
    }
    let containers: HashMap<_, _> = definitions.iter().map(|d| (d.symbol.as_str(), d)).collect();
    let mut edges = Vec::new();
    for row in references {
        let qualifier = rust_target(&row, &containers);
        let renamed = aliases.get(&row.path).is_some_and(|names| {
            names.contains(
                qualifier
                    .as_ref()
                    .map_or(row.name.as_str(), |parts| parts[0].as_str()),
            )
        });
        let unresolved =
            unvalidated.contains(&row.symbol) || (row.layer == Layer::Syntactic && renamed);
        let targets = if row.layer == Layer::Precise {
            vec![row.symbol.clone()]
        } else {
            let candidates = qualifier
                .clone()
                .filter(|_| rust_module(&row.path).is_some() && !renamed)
                .and_then(|path| rust.get(&path))
                .or_else(|| {
                    (!renamed || qualifier.is_some())
                        .then(|| names.get(row.name.as_str()))
                        .flatten()
                })
                .cloned()
                .unwrap_or_default();
            let mut ids: Vec<_> = candidates
                .iter()
                .filter(|r| {
                    let family = super::super::languages::family(&row.path);
                    family.is_some() && family == super::super::languages::family(&r.path)
                })
                .map(|r| r.symbol.clone())
                .collect();
            ids.sort();
            ids.dedup();
            ids
        };
        edges.push(Edge {
            source: row.container.clone(),
            targets,
            occurrence: row,
            unresolved,
        });
    }
    Graph {
        definitions,
        edges,
        relationships,
        gaps,
    }
}

// Concrete qualifiers match conventional Rust module paths; aliases remain name-only.
fn rust_module(path: &str) -> Option<Vec<String>> {
    let path = path.strip_prefix("src/")?.strip_suffix(".rs")?;
    if path.starts_with("bin/") {
        return None;
    }
    let mut parts: Vec<_> = path.split('/').map(str::to_owned).collect();
    if parts
        .last()
        .is_some_and(|p| matches!(p.as_str(), "main" | "lib" | "mod"))
    {
        parts.pop();
    }
    Some(parts)
}
fn rust_target(row: &Row, definitions: &HashMap<&str, &Row>) -> Option<Vec<String>> {
    if !row.path.ends_with(".rs") {
        return None;
    }
    let scope = row
        .container
        .as_deref()
        .and_then(|id| definitions.get(id))
        .and_then(|d| d.qualified.rsplit_once("::"))
        .map(|(scope, _)| format!("{scope}::"));
    let target = scope
        .as_ref()
        .and_then(|s| row.qualified.strip_prefix(s))
        .unwrap_or(&row.qualified);
    let parts: Vec<_> = target
        .split("::")
        .filter(|p| !p.is_empty())
        .map(str::to_owned)
        .collect();
    (parts.len() > 1).then_some(parts)
}

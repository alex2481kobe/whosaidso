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
        Ok(assemble(
            definitions,
            refs.rows,
            self.all_relationships()?,
            refs.gaps,
        ))
    }
    // Only selected callers and requested inbound names supply references. Name candidates
    // still resolve across the snapshot so narrowing cannot turn ambiguity into certainty.
    pub fn graph_in(&self, paths: &[String], incoming: &[String]) -> Result<Graph, String> {
        let mut refs = self.references_in(paths)?;
        for name in incoming {
            refs.rows.extend(self.candidates(name, false)?.rows);
        }
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
        let mut definitions = self.definitions_in(&source_paths)?.rows;
        let names: HashSet<_> = refs.rows.iter().map(|r| r.name.as_str()).collect();
        for name in names {
            definitions.extend(self.candidates(name, true)?.rows);
        }
        let mut seen = HashSet::new();
        definitions.retain(|r| {
            seen.insert((
                r.symbol.clone(),
                r.path.clone(),
                r.range.line,
                r.range.start,
            ))
        });
        let mut relationships = Vec::new();
        let sources: BTreeSet<_> = definitions
            .iter()
            .filter(|r| r.layer == Layer::Precise && source_paths.binary_search(&r.path).is_ok())
            .map(|r| r.symbol.as_str())
            .collect();
        for source in sources {
            relationships.extend(self.relationships(source, Direction::Forward)?);
        }
        Ok(assemble(definitions, refs.rows, relationships, refs.gaps))
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
) -> Graph {
    let mut names: HashMap<&str, Vec<&Row>> = HashMap::new();
    for d in &definitions {
        names.entry(&d.name).or_default().push(d);
    }
    let mut edges = Vec::new();
    for row in references {
        let targets = if row.layer == Layer::Precise {
            vec![row.symbol.clone()]
        } else {
            let candidates = names.get(row.name.as_str()).cloned().unwrap_or_default();
            let mut ids: Vec<_> = candidates.iter().map(|r| r.symbol.clone()).collect();
            ids.sort();
            ids.dedup();
            ids
        };
        edges.push(Edge {
            source: row.container.clone(),
            targets,
            occurrence: row,
        });
    }
    Graph {
        definitions,
        edges,
        relationships,
        gaps,
    }
}

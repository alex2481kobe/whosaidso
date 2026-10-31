// A hybrid graph retains exact precise identities and every syntax-name candidate.
use super::{
    Snapshot,
    reads::{Layer, Row},
    relationships::Relationship,
};
use std::collections::HashMap;

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
        Ok(assemble(definitions,refs.rows,self.all_relationships()?,refs.gaps))
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

fn assemble(definitions: Vec<Row>, references: Vec<Row>, relationships: Vec<Relationship>, gaps: Vec<String>) -> Graph {
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

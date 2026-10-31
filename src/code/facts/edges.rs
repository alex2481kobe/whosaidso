// Precise edges retain symbol identity; syntax edges retain every name-matched candidate.
use super::{
    Snapshot,
    reads::{Complete, Layer, Row},
};

pub enum Direction {
    Forward,
    Reverse,
}
pub struct Edge {
    pub source: Option<String>,
    pub targets: Vec<String>,
    pub occurrence: Row,
}
pub struct Edges {
    pub rows: Vec<Edge>,
    pub gaps: Vec<String>,
}
impl Snapshot {
    pub fn edges(&self, symbol: &str, direction: Direction) -> Result<Edges, String> {
        let definitions = self.definitions("")?;
        let Some(target) = definitions.rows.iter().find(|r| r.symbol == symbol) else {
            return Err("symbol is not in this snapshot".into());
        };
        let name = if target.qualified.is_empty() {
            &target.name
        } else {
            &target.qualified
        };
        let Complete { rows, mut gaps } = match direction {
            Direction::Reverse => self.references(name)?,
            Direction::Forward => self.all_references()?,
        };
        let mut edges = Vec::new();
        for mut row in rows {
            if matches!(direction, Direction::Forward) && row.container.as_deref() != Some(symbol) {
                continue;
            }
            if matches!(direction, Direction::Reverse)
                && row.layer == Layer::Precise
                && row.symbol != symbol
            {
                continue;
            }
            let candidates = if row.layer == Layer::Precise {
                vec![row.symbol.clone()]
            } else {
                self.definitions(&row.qualified)?
                    .rows
                    .into_iter()
                    .map(|r| r.symbol)
                    .collect::<Vec<_>>()
            };
            row.ambiguous = row.layer == Layer::Syntactic || candidates.len() != 1;
            edges.push(Edge {
                source: row.container.clone(),
                targets: candidates,
                occurrence: row,
            });
        }
        gaps.push("unresolved and ambiguous references retain candidate targets".into());
        Ok(Edges { rows: edges, gaps })
    }
}

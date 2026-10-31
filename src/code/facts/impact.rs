// Breadth-first reachability terminates cycles and keeps each diamond endpoint once.
use super::{
    Snapshot,
    edges::{Direction, Graph},
    reads::{Layer, Row},
};
use std::collections::{BTreeMap, HashMap, HashSet, VecDeque};
pub struct Reach {
    pub symbol: String,
    pub distance: usize,
    pub ambiguous: bool,
    pub implementation: bool,
    pub layer: Layer,
}
pub struct Impact {
    pub seeds: Vec<Row>,
    pub rows: Vec<Reach>,
    pub ambiguous: bool,
    pub depth_limited: bool,
    pub unresolved: usize,
    pub implementations: usize,
    pub graph: Graph,
}
impl Snapshot {
    pub fn impact(
        &self,
        value: &str,
        direction: Direction,
        depth: usize,
        implementations: bool,
    ) -> Result<Impact, String> {
        let graph = self.graph()?;
        let path = self.files.entries.iter().any(|f| f.path == value);
        let selected = if path {
            self.outline(value)?
        } else {
            self.select(value)?
        };
        let ambiguous = !path && selected.len() > 1;
        Ok(walk(
            graph,
            selected,
            ambiguous,
            direction,
            depth,
            implementations,
        ))
    }
    pub fn impact_rows(
        &self,
        rows: Vec<Row>,
        direction: Direction,
        depth: usize,
        implementations: bool,
    ) -> Result<Impact, String> {
        Ok(walk(
            self.graph()?,
            rows,
            false,
            direction,
            depth,
            implementations,
        ))
    }
}
fn walk(
    graph: Graph,
    selected: Vec<Row>,
    ambiguous: bool,
    direction: Direction,
    depth: usize,
    implementations: bool,
) -> Impact {
    let seeds: HashSet<_> = selected.iter().map(|r| r.symbol.clone()).collect();
    let mut adjacency: HashMap<&str, Vec<(&str, bool, bool, Layer)>> = HashMap::new();
    let mut unresolved = 0;
    for e in &graph.edges {
        let Some(source) = e.source.as_deref() else {
            unresolved += 1;
            continue;
        };
        if e.targets.is_empty() {
            unresolved += 1;
        }
        for target in &e.targets {
            let (from, to) = match direction {
                Direction::Forward => (source, target.as_str()),
                Direction::Reverse => (target.as_str(), source),
            };
            adjacency.entry(from).or_default().push((
                to,
                e.occurrence.ambiguous || e.targets.len() > 1,
                false,
                e.occurrence.layer,
            ));
        }
    }
    let mut implementation_count = 0;
    for r in &graph.relationships {
        if !r.is_implementation {
            continue;
        }
        if seeds.contains(&r.source) || seeds.contains(&r.target) {
            implementation_count += 1;
        }
        if implementations {
            let (from, to) = match direction {
                Direction::Forward => (r.source.as_str(), r.target.as_str()),
                Direction::Reverse => (r.target.as_str(), r.source.as_str()),
            };
            adjacency
                .entry(from)
                .or_default()
                .push((to, false, true, Layer::Precise));
        }
    }
    let mut found: BTreeMap<String, Reach> = BTreeMap::new();
    let mut visited = seeds.clone();
    let mut ordered: Vec<_> = seeds.iter().map(String::as_str).collect();
    ordered.sort_unstable();
    let mut queue: VecDeque<_> = if ambiguous {
        VecDeque::new()
    } else {
        ordered.into_iter().map(|s| (s, 0, false)).collect()
    };
    let mut depth_limited = false;
    while let Some((symbol, distance, uncertain)) = queue.pop_front() {
        let Some(next) = adjacency.get(symbol) else {
            continue;
        };
        if distance == depth {
            depth_limited |= !next.is_empty();
            continue;
        }
        for &(to, candidate, implementation, layer) in next {
            let ambiguous = uncertain || candidate;
            if seeds.contains(to) {
                continue;
            }
            if let Some(row) = found.get_mut(to) {
                row.ambiguous |= ambiguous;
                row.implementation |= implementation;
            }
            if !visited.insert(to.to_owned()) {
                continue;
            }
            found.insert(
                to.to_owned(),
                Reach {
                    symbol: to.to_owned(),
                    distance: distance + 1,
                    ambiguous,
                    implementation,
                    layer,
                },
            );
            queue.push_back((to, distance + 1, ambiguous));
        }
    }
    let mut rows: Vec<_> = found.into_values().collect();
    rows.sort_by(|a, b| (a.distance, &a.symbol).cmp(&(b.distance, &b.symbol)));
    Impact {
        seeds: selected,
        rows,
        ambiguous,
        depth_limited,
        unresolved,
        implementations: implementation_count,
        graph,
    }
}

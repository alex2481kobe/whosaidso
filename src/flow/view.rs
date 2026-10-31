use super::model::{Flow, Node, Risk, Step};
use std::collections::{BTreeMap, BTreeSet};

pub struct VisibleStep<'a> {
    pub step: &'a Step,
    pub from: String,
    pub to: String,
}
pub struct Layer<'a> {
    pub nodes: Vec<(&'a Node, usize)>,
    pub steps: Vec<VisibleStep<'a>>,
    pub risks: Vec<(&'a str, &'a Risk)>,
    pub frontier: usize,
}

pub fn layer<'a>(flow: &'a Flow, parent: Option<&str>) -> Result<Layer<'a>, String> {
    let parents: BTreeMap<_, _> = flow
        .nodes
        .iter()
        .map(|n| (n.id.as_str(), n.parent.as_deref()))
        .collect();
    if let Some(id) = parent
        && !parents.contains_key(id)
    {
        return Err(format!("unknown layer {id:?}"));
    }
    let visible: BTreeSet<_> = flow
        .nodes
        .iter()
        .filter(|n| n.parent.as_deref() == parent)
        .map(|n| n.id.as_str())
        .collect();
    let project = |id: &'a str| {
        let mut current = Some(id);
        while let Some(node) = current {
            if visible.contains(node) {
                return Some(node);
            }
            current = parents.get(node).copied().flatten();
        }
        None
    };
    let nodes = flow
        .nodes
        .iter()
        .filter(|n| visible.contains(n.id.as_str()))
        .map(|n| {
            let children = flow
                .nodes
                .iter()
                .filter(|child| child.parent.as_deref() == Some(&n.id))
                .count();
            (n, children)
        })
        .collect();
    let steps = flow
        .steps
        .iter()
        .filter_map(|step| {
            let from = project(&step.from);
            let to = project(&step.to);
            if from.is_none() && to.is_none() {
                return None;
            }
            // A step inside one node belongs to that node's own layer.
            let within = |id: &str| flow.nodes.iter().any(|n| n.parent.as_deref() == Some(id));
            if from.is_some() && from == to && within(from.unwrap_or_default()) {
                return None;
            }
            Some(VisibleStep {
                step,
                from: from
                    .map(str::to_owned)
                    .unwrap_or_else(|| format!("outside:{}", step.from)),
                to: to
                    .map(str::to_owned)
                    .unwrap_or_else(|| format!("outside:{}", step.to)),
            })
        })
        .collect();
    let risks = flow
        .nodes
        .iter()
        .filter(|n| project(&n.id).is_some())
        .flat_map(|n| n.risks.iter().map(move |risk| (n.id.as_str(), risk)))
        .collect();
    let frontier = flow
        .nodes
        .iter()
        .filter(|n| !visible.contains(n.id.as_str()) && project(&n.id).is_some())
        .count();
    Ok(Layer {
        nodes,
        steps,
        risks,
        frontier,
    })
}

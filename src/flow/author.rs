use super::{model::Flow, schema, validate};
use serde_json::Value;

pub enum Change {
    Replace(Value),
    Put { kind: String, value: Value },
    Remove { kind: String, id: String },
    Delete,
}

pub fn apply(current: Option<Flow>, change: Change) -> Result<Option<Flow>, String> {
    if matches!(change, Change::Delete) {
        return Ok(None);
    }
    let mut value = serde_json::to_value(current.unwrap_or_default()).map_err(|e| e.to_string())?;
    match change {
        Change::Replace(replacement) => value = replacement,
        Change::Put { kind, value: part } => match kind.as_str() {
            "covers" => value["covers"] = part,
            "node" | "step" => {
                let key = if kind == "node" { "nodes" } else { "steps" };
                let items = value[key].as_array_mut().unwrap();
                if let Some(existing) = items
                    .iter_mut()
                    .find(|v| v.get("id").is_some() && v.get("id") == part.get("id"))
                {
                    *existing = part;
                } else {
                    items.push(part);
                }
            }
            _ => return Err("expected covers, node or step".into()),
        },
        Change::Remove { kind, id } => {
            let key = match kind.as_str() {
                "node" => "nodes",
                "step" => "steps",
                _ => return Err("expected node or step".into()),
            };
            let items = value[key].as_array_mut().unwrap();
            let position = items
                .iter()
                .position(|v| v["id"].as_str() == Some(&id))
                .ok_or_else(|| format!("unknown {kind} {id:?}"))?;
            items.remove(position);
        }
        Change::Delete => unreachable!(),
    }
    let (flow, mut problems) = schema::decode(&value);
    problems.extend(validate::validate(&flow));
    if problems.is_empty() {
        Ok(Some(flow))
    } else {
        Err(problems.join("\n"))
    }
}

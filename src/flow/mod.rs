mod author;
mod model;
mod map;
mod compare;
mod hops;
mod pattern;
mod schema;
mod store;
mod validate;
mod view;

use std::path::Path;

pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    match args {
        [cmd, id] if cmd == "compare" => {
            let record = store::load(root, Some(id))?.pop().ok_or("unknown flow")?;
            if !record.problems.is_empty() { return Err(record.problems.join("\n")); }
            let snapshot = crate::code::facts::Snapshot::current(root)?;
            println!("{}", compare::compare(&record.flow.ok_or("unreadable flow")?, &snapshot)?);
            Ok(())
        }
        [cmd, rest @ ..] if cmd == "map" && rest.len() <= 1 => {
            let snapshot = crate::code::facts::Snapshot::current(root)?;
            println!("{}", map::layer(&snapshot, rest.first().map(String::as_str), 0)?);
            Ok(())
        }
        [cmd, rest @ ..] if cmd == "check" && rest.len() <= 1 => {
            let records = store::load(root, rest.first().map(String::as_str))?;
            let mut broken = false;
            for record in records {
                let state = if record.problems.is_empty() {
                    "authored"
                } else {
                    "broken"
                };
                let (nodes, steps) = record
                    .flow
                    .as_ref()
                    .map_or((0, 0), |f| (f.nodes.len(), f.steps.len()));
                println!(
                    "{} {state} nodes={nodes} steps={steps} comparison=unknown",
                    record.id
                );
                for problem in record.problems {
                    println!("  {problem}");
                    broken = true;
                }
            }
            if broken { Err(String::new()) } else { Ok(()) }
        }
        [cmd, rest @ ..] if cmd == "show" && rest.len() <= 2 => {
            let records = store::load(root, rest.first().map(String::as_str))?;
            let mut broken = false;
            for record in records {
                if !record.problems.is_empty() {
                    println!("{} broken revision={}", record.id, record.revision);
                    for problem in record.problems { println!("  {problem}"); }
                    broken = true;
                    continue;
                }
                let flow = record.flow.ok_or("flow has no readable data")?;
                let parent = rest.get(1).map(String::as_str);
                let layer = view::layer(&flow, parent)?;
                println!("{} authored layer={} revision={} frontier={} comparison=unknown", record.id, parent.unwrap_or("top"), record.revision, layer.frontier);
                for (node, children) in layer.nodes {
                    println!("node {} type={} status={} children={} shared={} note={}", node.id, node.r#type, node.status, children, node.shared, node.note);
                    if !node.paths.is_empty() { println!("  paths={}", node.paths.join(",")); }
                    if !node.symbols.is_empty() { println!("  symbols={}", node.symbols.join(",")); }
                    if !node.refs.is_empty() { println!("  refs={}", node.refs.join(",")); }
                    if let Some(goal) = &node.goal { println!("  goal={goal}"); }
                }
                for visible in layer.steps {
                    println!("step {} {} -> {} when={}", visible.step.id, visible.from, visible.to, visible.step.when);
                }
                for (node, risk) in layer.risks {
                    let m = &risk.mitigation;
                    let (kind, target) = if let Some(id) = &m.step { ("step", id) }
                        else if let Some(id) = &m.goal { ("goal", id) }
                        else { ("note", m.note.as_ref().ok_or("risk has no mitigation")?) };
                    println!("risk {} node={node} mitigation={kind}:{target} note={}", risk.id, risk.note);
                }
            }
            if broken { Err(String::new()) } else { Ok(()) }
        }
        [cmd, id, json] if cmd == "put" => {
            let value = serde_json::from_str(json).map_err(|e| e.to_string())?;
            store::change(root, id, author::Change::Replace(value))?;
            println!("{id} saved");
            Ok(())
        }
        [cmd, id, kind, json] if cmd == "put" => {
            let value = serde_json::from_str(json).map_err(|e| e.to_string())?;
            store::change(root, id, author::Change::Put { kind: kind.clone(), value })?;
            println!("{id} saved");
            Ok(())
        }
        [cmd, id] if cmd == "rm" => {
            store::change(root, id, author::Change::Delete)?;
            println!("{id} removed");
            Ok(())
        }
        [cmd, id, kind, item] if cmd == "rm" => {
            store::change(root, id, author::Change::Remove { kind: kind.clone(), id: item.clone() })?;
            println!("{id} removed {kind} {item}");
            Ok(())
        }
        _ => Err("usage: flow check [<id>] | show [<id> [<parent>]] | put <id> [covers|node|step] <json> | rm <id> [node|step <item>]".into()),
    }
}

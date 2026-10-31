use super::{compare, map, store};
use crate::code::facts::Snapshot;
use serde_json::{Value, json};
use std::path::Path;

pub fn screen(
    root: &Path,
    id: Option<&str>,
    parent: Option<&str>,
    offset: usize,
    branch: Option<&str>,
) -> Result<Value, String> {
    let parent = parent.filter(|p| !p.is_empty());
    let (snapshot, commit) = snapshot(root, branch)?;
    let mut result = if id == Some("$map") {
        map::layer(&snapshot, parent, offset)?
    } else {
        let records = if let Some(commit) = &commit {
            store::committed(root, commit, id)?
        } else {
            store::load(root, id)?
        };
        if let Some(id) = id {
            let record = records.into_iter().next().ok_or("unknown flow")?;
            if !record.problems.is_empty() {
                json!({"id":id,"revision":format!("{}:{}",record.revision,snapshot.id),"state":"unknown","problems":record.problems,"nodes":[],"steps":[]})
            } else {
                let flow = record.flow.ok_or("unreadable flow")?;
                super::authored::layer(&flow, &snapshot, id, &record.revision, parent, offset)?
            }
        } else {
            let revision = format!(
                "{}:{}",
                snapshot.id,
                crate::record::sha256_hex(
                    records
                        .iter()
                        .map(|r| format!("{}:{}", r.id, r.revision))
                        .collect::<String>()
                        .as_bytes()
                )
            );
            let total = records.len();
            let rows: Vec<_> = records
                .into_iter()
                .skip(offset)
                .take(map::PAGE)
                .map(|r| json!({"id":r.id,"problems":r.problems}))
                .collect();
            json!({"flows":rows,"revision":revision,"next":(offset+map::PAGE<total).then_some(offset+map::PAGE)})
        }
    };
    if result.get("freshness").is_none() {
        result["freshness"] = json!(snapshot.freshness()?);
    }
    result["committed_only"] = json!(branch.is_some());
    result["branch"] = json!(branch);
    result["branches"] = json!(
        crate::git::run(
            root,
            &["for-each-ref", "--format=%(refname:short)", "refs/heads"]
        )?
        .lines()
        .collect::<Vec<_>>()
    );
    Ok(result)
}

fn snapshot(root: &Path, branch: Option<&str>) -> Result<(Snapshot, Option<String>), String> {
    let commit = branch
        .map(|b| crate::git::local_revision(root, b))
        .transpose()?;
    let snapshot = if let Some(commit) = &commit {
        Snapshot::branch(root, commit)?
    } else {
        Snapshot::current(root)?
    };
    Ok((snapshot, commit))
}

pub fn comparison(root: &Path, id: &str, branch: Option<&str>) -> Result<Value, String> {
    let (snapshot, commit) = snapshot(root, branch)?;
    let records = if let Some(commit) = &commit {
        store::committed(root, commit, Some(id))?
    } else {
        store::load(root, Some(id))?
    };
    let record = records.into_iter().next().ok_or("unknown flow")?;
    if !record.problems.is_empty() {
        return Err(record.problems.join("\n"));
    }
    let mut value =
        compare::bounded(compare::compare(&record.flow.ok_or("unreadable flow")?, &snapshot)?.0);
    value["committed_only"] = json!(branch.is_some());
    value["branch"] = json!(branch);
    Ok(value)
}

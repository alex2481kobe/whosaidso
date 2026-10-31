use super::{compare, store};
use crate::code::facts::Snapshot;
use std::path::Path;

pub fn check_drift(root: &Path) -> Result<(), String> {
    let records = store::load(root, None)?;
    if records.is_empty() {
        return Ok(());
    }
    let snapshot = Snapshot::current(root)?;
    let mut faults = vec![];
    for record in records {
        if !record.problems.is_empty() {
            faults.push(format!(
                "{} broken: {}",
                record.id,
                record.problems.join("; ")
            ));
            continue;
        }
        let report = compare::compare(&record.flow.ok_or("unreadable flow")?, &snapshot)?;
        let state = report["state"].as_str().ok_or("flow has no verdict")?;
        if !["built", "planned"].contains(&state) {
            faults.push(format!("{} {state}", record.id));
        }
    }
    if faults.is_empty() {
        Ok(())
    } else {
        Err(faults.join("\n"))
    }
}

pub fn change_token(root: &Path) -> Result<String, String> {
    let snapshot = Snapshot::current(root)?;
    let mut token = snapshot.change_token;
    for record in store::load(root, None)? {
        token.push_str(&format!(
            "{}:{}:{:?}",
            record.id, record.revision, record.problems
        ));
    }
    Ok(crate::record::sha256_hex(token.as_bytes()))
}

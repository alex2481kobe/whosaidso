// Review compares immutable source snapshots, optionally narrowed by a unified diff.
mod changes;
mod diff;
use crate::code::facts::{
    Snapshot,
    edges::Direction,
    reads::{Layer, Row},
};
use std::{collections::BTreeSet, io::Read, path::Path};
pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    let mut base = "HEAD";
    let mut input = None;
    let mut limit = 20;
    let mut offset = 0;
    let mut depth = 1;
    let mut i = 0;
    while i < args.len() {
        let value = args.get(i + 1).ok_or("review flag needs a value")?;
        match args[i].as_str() {
            "--base" => base = value,
            "--diff" => input = Some(value.as_str()),
            "--limit" => {
                limit = number(value)?;
                if !(1..=100).contains(&limit) {
                    return Err("review limit must be 1..100".into());
                }
            }
            "--offset" => offset = number(value)?,
            "--depth" => {
                depth = number(value)?;
                if depth > 100 {
                    return Err("review depth must be 0..100".into());
                }
            }
            _ => return Err(format!("unknown review flag: {}", args[i])),
        }
        i += 2;
    }
    let before = Snapshot::branch(root, base)?;
    let after = Snapshot::current(root)?;
    let changes = match input {
        Some(path) => {
            let mut text = String::new();
            if path == "-" {
                std::io::stdin()
                    .take(8 * 1024 * 1024 + 1)
                    .read_to_string(&mut text)
                    .map_err(|e| e.to_string())?;
            } else {
                text =
                    std::fs::read_to_string(root.join(path)).map_err(|e| format!("diff: {e}"))?;
            }
            if text.len() > 8 * 1024 * 1024 {
                return Err("diff exceeds 8 MiB input budget".into());
            }
            diff::parse(&text)?
        }
        None => changes::compare(&before, &after),
    };
    let mut rows = Vec::new();
    let old_defs = before.definitions("")?.rows;
    let new_defs = after.definitions("")?.rows;
    let mut old_touched = Vec::new();
    let mut new_touched = Vec::new();
    for change in &changes {
        rows.push(change.description());
        for (snapshot, defs, prefix, touched) in [
            (&before, &old_defs, "before", &mut old_touched),
            (&after, &new_defs, "after", &mut new_touched),
        ] {
            let mut count = 0;
            for r in defs
                .iter()
                .filter(|r| change.touches(r, prefix == "before"))
            {
                rows.push(format!("{prefix} {}", definition(r)));
                touched.push(r.clone());
                count += 1;
            }
            let path = if prefix == "before" {
                &change.old
            } else {
                &change.new
            };
            if count == 0
                && let Some(path) = path
            {
                let indexed = snapshot
                    .coverage()?
                    .iter()
                    .any(|c| c.path == *path && (c.syntax == "indexed" || c.precise == "fresh"));
                rows.push(format!(
                    "{prefix} {path}: {}; absence is not proven",
                    if indexed {
                        "none recorded in changed ranges"
                    } else {
                        "not indexed"
                    }
                ));
            }
        }
    }
    let mut gaps = BTreeSet::new();
    for (snapshot, seeds, side) in [
        (&before, old_touched, "before"),
        (&after, new_touched, "after"),
    ] {
        let impact = snapshot.impact_rows(seeds, Direction::Reverse, depth, true)?;
        for reach in &impact.rows {
            if let Some(row) = impact
                .graph
                .definitions
                .iter()
                .find(|r| r.symbol == reach.symbol)
            {
                rows.push(format!(
                    "affected {side} {} distance={}{}",
                    definition(row),
                    reach.distance,
                    if reach.ambiguous {
                        " candidate=by-name"
                    } else {
                        ""
                    }
                ));
            }
        }
        if impact.depth_limited {
            gaps.insert(format!(
                "depth limit {depth}; additional reachability is unknown"
            ));
        }
        gaps.extend(impact.graph.gaps);
    }
    println!(
        "review: before={} current={} files={} rows={} offset={offset} limit={limit}",
        before.id,
        after.id,
        changes.len(),
        rows.len()
    );
    for row in rows.iter().skip(offset).take(limit) {
        println!("{}", crate::text::cut(row, 500));
    }
    println!(
        "shown {} of {}",
        rows.len().saturating_sub(offset).min(limit),
        rows.len()
    );
    if rows.is_empty() {
        println!("none recorded; absence is not proven");
    }
    for gap in gaps.iter().take(6) {
        println!("gap: {gap}");
    }
    println!(
        "gap: before uses committed syntax; current uses the fresh hybrid; edges are references, not proven runtime calls"
    );
    println!(
        "gap: {}",
        if input.is_some() {
            "diff scope supplied by caller; its source lines were not verified against snapshots"
        } else {
            "file-scope comparison; only identical blobs pair as renames"
        }
    );
    Ok(())
}
fn definition(row: &Row) -> String {
    format!(
        "{}:{} {} [{}]",
        row.path,
        row.range.line,
        row.qualified,
        if row.layer == Layer::Precise {
            "precise"
        } else {
            "syntactic"
        }
    )
}
fn number(value: &str) -> Result<usize, String> {
    value
        .parse()
        .map_err(|_| "review budget must be a non-negative integer".into())
}

// Snapshot comparison includes untracked files and pairs only unambiguous identical-blob renames.
use crate::code::facts::{Snapshot, reads::Row};
use std::collections::{BTreeMap, HashSet};
#[derive(Default)]
pub(super) struct Change {
    pub old: Option<String>,
    pub new: Option<String>,
    pub old_ranges: Vec<(i64, i64)>,
    pub new_ranges: Vec<(i64, i64)>,
}
impl Change {
    pub fn description(&self) -> String {
        match (&self.old, &self.new) {
            (Some(a), Some(b)) if a != b => format!("rename {a} -> {b}"),
            (Some(a), Some(_)) => format!("modify {a}"),
            (Some(a), None) => format!("delete {a}"),
            (None, Some(b)) => format!("add {b}"),
            _ => "unknown file change".into(),
        }
    }
    pub fn touches(&self, row: &Row, before: bool) -> bool {
        let (path, ranges) = if before {
            (&self.old, &self.old_ranges)
        } else {
            (&self.new, &self.new_ranges)
        };
        if path.as_deref() != Some(&row.path) {
            return false;
        }
        let range = row.enclosing.as_ref().unwrap_or(&row.range);
        ranges.is_empty()
            || ranges.iter().any(|(start, count)| {
                range.line <= start + count.max(&1) - 1 && range.end_line >= *start
            })
    }
}
pub(super) fn compare(before: &Snapshot, after: &Snapshot) -> Vec<Change> {
    let old: BTreeMap<_, _> = before
        .files
        .entries
        .iter()
        .map(|f| (&f.path, &f.blob))
        .collect();
    let new: BTreeMap<_, _> = after
        .files
        .entries
        .iter()
        .map(|f| (&f.path, &f.blob))
        .collect();
    let deleted: Vec<_> = old.iter().filter(|(p, _)| !new.contains_key(*p)).collect();
    let added: Vec<_> = new.iter().filter(|(p, _)| !old.contains_key(*p)).collect();
    let mut paired = HashSet::new();
    let mut changes = Vec::new();
    for (path, blob) in &old {
        if let Some(current) = new.get(path) {
            if current != blob {
                changes.push(Change {
                    old: Some((*path).clone()),
                    new: Some((*path).clone()),
                    ..Change::default()
                });
            }
        } else {
            let matches: Vec<_> = added.iter().filter(|(_, b)| *b == blob).collect();
            let unique = deleted.iter().filter(|(_, b)| *b == blob).count() == 1;
            let target = (matches.len() == 1 && unique).then(|| (*matches[0].0).clone());
            if let Some(p) = &target {
                paired.insert(p.clone());
            }
            changes.push(Change {
                old: Some((*path).clone()),
                new: target,
                ..Change::default()
            });
        }
    }
    for (path, _) in added {
        if !paired.contains(*path) {
            changes.push(Change {
                new: Some((*path).clone()),
                ..Change::default()
            });
        }
    }
    changes
}

// Selection preserves find's ordering; all output, including omission metadata, counts against the budget.
use crate::{find, text::prefix};
use std::path::Path;

fn footer(names: &[String], outside: usize, budget: usize) -> String {
    if names.is_empty() && outside == 0 {
        return "omitted: none\n".into();
    }
    let mut out = "omitted: ".to_owned();
    for (i, name) in names.iter().enumerate() {
        let rest = format!("; {} more matches\n", names.len() - i - 1 + outside);
        let available = budget.saturating_sub(out.len() + rest.len());
        if name.len() + 2 > available {
            out.push_str(prefix(name, available.saturating_sub(3)));
            out.push_str("...");
            out.push_str(&rest);
            return out;
        }
        out.push_str(name);
        out.push_str("; ");
    }
    if outside > 0 {
        out.push_str(&format!(
            "{outside} additional matches outside ranked window"
        ));
    }
    out.push('\n');
    out
}
pub(super) fn build(
    root: &Path,
    topic: &[String],
    budget: usize,
) -> Result<(String, Option<String>), String> {
    let results = find::ranked(root, topic, &["file", "symbol", "goal", "note"], 64)?;
    if results.hits.is_empty() {
        return Ok(("context: no indexed match\n".into(), results.notice));
    }
    let reserve = (budget / 3).clamp(64, 512);
    let capacity = budget - reserve;
    let mut output = String::new();
    let mut omitted = Vec::new();
    for hit in results.hits.iter().take(64) {
        let label = find::label(hit);
        let label = if hit.kind == "file" {
            format!("file {label}")
        } else {
            label
        };
        let mut heading = format!("{label}\n");
        if hit.kind == "symbol" {
            heading.push_str(&format!(
                "file {}\n",
                hit.source.strip_prefix("file:").unwrap_or(&hit.source)
            ));
        }
        let available = capacity.saturating_sub(output.len());
        if available < heading.len() + 25 {
            omitted.push(label);
            continue;
        }
        output.push_str(&heading);
        let detail = results.detail(hit)?;
        let available = capacity.saturating_sub(output.len() + 25).min(1600);
        output.push_str(prefix(&detail, available));
        if detail.len() > available {
            output.push_str("\n[excerpt truncated]");
            omitted.push(format!("{label} (excerpt)"));
        }
        output.push_str("\n\n");
    }
    let outside = (results.total as usize).saturating_sub(results.hits.len().min(64));
    if results.lower_bound && omitted.is_empty() && outside == 0 {
        output.push_str("omitted: more matches may exist\n");
    } else {
        let suffix = if results.lower_bound {
            " [lower bound]"
        } else {
            ""
        };
        let summary = footer(&omitted, outside, reserve.saturating_sub(suffix.len()));
        output.push_str(summary.trim_end_matches('\n'));
        output.push_str(suffix);
        output.push('\n');
    }
    Ok((output, results.notice))
}

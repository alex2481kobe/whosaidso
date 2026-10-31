// Selection preserves find's ordering; all output, including omission metadata, counts against the budget.
use crate::find;
use std::path::Path;

fn prefix(text: &str, bytes: usize) -> &str {
    let mut end = bytes.min(text.len());
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    &text[..end]
}
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
pub(super) fn build(root: &Path, topic: &[String], budget: usize) -> Result<String, String> {
    let results = find::ranked(root, topic, &["file", "symbol", "goal", "note"], 64)?;
    if results.hits.is_empty() {
        return Ok("context: no indexed match\n".into());
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
        let available = capacity.saturating_sub(output.len());
        if available < label.len() + 32 {
            omitted.push(label);
            continue;
        }
        output.push_str(&label);
        output.push('\n');
        if hit.kind == "symbol" {
            output.push_str(&format!(
                "file {}\n",
                hit.source.strip_prefix("file:").unwrap_or(&hit.source)
            ));
        }
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
    output.push_str(&footer(&omitted, outside, reserve));
    Ok(output)
}

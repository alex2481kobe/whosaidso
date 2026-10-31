// Search answers carry a source declaration without turning into context packs.
use super::{label, pool::Hit, query::Results};
use crate::text::prefix;
const ROW_BYTES: usize = 384;
const ANSWER_BYTES: usize = 4096;

fn row(results: &Results, hit: &Hit) -> Result<String, String> {
    let label = label(hit);
    let label = if label.len() + 1 > ROW_BYTES {
        format!("{}... [label truncated]", prefix(&label, ROW_BYTES - 24))
    } else {
        label
    };
    let mut out = format!("{label}\n");
    if hit.kind == "symbol" {
        let path = hit
            .source
            .strip_prefix("file:")
            .ok_or("find: invalid symbol source")?;
        let line = hit
            .key
            .strip_prefix(&format!("symbol:{path}:"))
            .and_then(|s| s.split_once(':'))
            .and_then(|(n, _)| n.parse::<usize>().ok())
            .filter(|n| *n > 0)
            .ok_or("find: invalid symbol location")?;
        let source = results.source_line(&hit.source, line)?;
        let signature = source
            .as_deref()
            .unwrap_or("signature unavailable: no indexed source line");
        let signature = signature.split('{').next().unwrap_or(signature).trim();
        let available = ROW_BYTES.saturating_sub(out.len() + 3).min(160);
        if available >= 4 {
            let signature = if signature.len() > available {
                format!("{}...", prefix(signature, available - 3))
            } else {
                signature.to_owned()
            };
            out.push_str(&format!("  {signature}\n"));
        }
    }
    Ok(out)
}

pub(super) fn answer(results: &Results, limit: usize) -> Result<String, String> {
    let (mut out, mut shown) = (String::new(), 0);
    for hit in results.hits.iter().take(limit) {
        let row = row(results, hit)?;
        if out.len() + row.len() > ANSWER_BYTES - 128 {
            break;
        }
        out.push_str(&row);
        shown += 1;
    }
    let bound = if results.lower_bound { "at least " } else { "" };
    let capped = if shown < results.hits.len().min(limit) {
        "; output budget 4096 bytes"
    } else {
        ""
    };
    out.push_str(&format!(
        "shown {shown} of {bound}{}{capped}\n",
        results.total
    ));
    Ok(out)
}

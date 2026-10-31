// Budgeted presentation for symbol bodies and file structure.
use super::facts::{
    Snapshot,
    reads::{Layer, Row},
};
pub(super) fn label(row: &Row) -> &'static str {
    match row.layer {
        Layer::Precise => "precise",
        Layer::Syntactic => "syntactic",
    }
}
pub(super) fn location(row: &Row) -> String {
    let r = row.enclosing.as_ref().unwrap_or(&row.range);
    format!(
        "{}:{}:{}-{}:{}",
        row.path,
        r.line,
        r.start.unwrap_or(0),
        r.end_line,
        r.stop.unwrap_or(0)
    )
}
pub(super) fn answer(snapshot: &Snapshot, command: &str, value: &str) -> Result<String, String> {
    let rows = if command == "show" {
        snapshot.select(value)?
    } else {
        snapshot.outline(value)?
    };
    let mut out = Vec::new();
    if rows.is_empty() {
        let coverage = snapshot.coverage()?;
        if let Some(c) = coverage
            .iter()
            .find(|c| c.path == value && c.syntax != "indexed" && c.precise != "fresh")
        {
            return Ok(format!("{}: not indexed ({})", c.path, c.syntax));
        }
        return Ok("code: none recorded; absence is not proven".into());
    }
    if command == "show" && rows.len() != 1 {
        out.push(format!(
            "ambiguous: {} definitions; use path::name or symbol id",
            rows.len()
        ));
    }
    for row in rows.iter().take(20) {
        out.push(format!(
            "{} {} {} [{}]",
            location(row),
            row.kind,
            row.qualified,
            label(row)
        ));
        if command == "show" && rows.len() == 1 {
            match snapshot.symbol_body(row)? {
                Some((_, body)) => {
                    let text = String::from_utf8(body).map_err(|e| e.to_string())?;
                    let mut shown = String::new();
                    for line in text.lines().take(100) {
                        if shown.len() + line.len() + 1 > 16_384 {
                            break;
                        }
                        shown.push_str(line);
                        shown.push('\n');
                    }
                    if shown.trim_end_matches('\n') != text.trim_end_matches('\n') {
                        shown.push_str("body truncated: budget 100 lines / 16384 bytes\n");
                    }
                    out.push(shown);
                }
                None => {
                    out.push("body unavailable: no recorded enclosing range or source bytes".into())
                }
            }
        }
    }
    if rows.len() > 20 {
        out.push(format!("shown 20 of {}", rows.len()));
    }
    Ok(out.join("\n"))
}

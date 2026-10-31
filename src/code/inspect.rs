// Budgeted presentation for symbol bodies and file structure.
use super::contracts::{DEFAULT_LIMIT, GAP_LIMIT, ROW_BYTES};
const BODY_LINES: usize = 100;
const BODY_BYTES: usize = 16_384;
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
    let encoding = match r.encoding {
        0 => "unspecified",
        1 => "utf8",
        2 => "utf16",
        3 => "utf32",
        _ => "unknown",
    };
    format!(
        "{}:{}:{}-{}:{} encoding={encoding}",
        row.path,
        r.line,
        r.start.unwrap_or(0),
        r.end_line,
        r.stop.unwrap_or(0)
    )
}
pub(super) fn answer(snapshot: &Snapshot, command: &str, value: &str) -> Result<String, String> {
    let rows = if command == "show" {
        select(snapshot, value)?
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
        let (precise, syntax, missing) = snapshot.coverage_summary()?;
        let mut out = vec![if (precise + syntax == 0 && missing > 0)
            || (command == "outline" && !snapshot.files.entries.iter().any(|f| f.path == value))
        {
            "code: not indexed; no searchable code coverage".to_string()
        } else {
            "code: none recorded; absence is not proven".to_string()
        }];
        out.extend(
            snapshot
                .blind_spots(&[])?
                .iter()
                .take(GAP_LIMIT)
                .map(|g| format!("gap: {g}")),
        );
        return Ok(out.join("\n"));
    }
    if command == "show" && rows.len() != 1 {
        out.push(format!(
            "ambiguous: {} definitions; use path::name or symbol id",
            rows.len()
        ));
    }
    for row in rows.iter().take(DEFAULT_LIMIT) {
        out.push(crate::text::cut(
            &format!(
                "{} {} {} [{}]{}",
                location(row),
                row.kind,
                row.qualified,
                label(row),
                if command == "show" && rows.len() > 1 {
                    format!(" id={}", row.symbol)
                } else {
                    String::new()
                }
            ),
            ROW_BYTES,
        ));
        if command == "show" && rows.len() == 1 {
            match snapshot.symbol_body(row)? {
                Some((_, body)) => {
                    let text = String::from_utf8(body).map_err(|e| e.to_string())?;
                    let mut shown = String::new();
                    for line in text.lines().take(BODY_LINES) {
                        if shown.len() + line.len() + 1 > BODY_BYTES {
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
    if rows.len() > DEFAULT_LIMIT {
        out.push(format!("shown {DEFAULT_LIMIT} of {}", rows.len()));
    }
    let gaps = snapshot.blind_spots(&rows)?;
    out.extend(gaps.iter().take(GAP_LIMIT).map(|g| format!("gap: {g}")));
    if gaps.len() > GAP_LIMIT {
        out.push(format!("gaps: {} omitted", gaps.len() - GAP_LIMIT));
    }
    Ok(out.join("\n"))
}

fn select(snapshot: &Snapshot, value: &str) -> Result<Vec<Row>, String> {
    let value = value.trim();
    let value = value
        .strip_prefix("symbol ")
        .or_else(|| value.strip_prefix("symbol:"))
        .unwrap_or(value);
    let value = value.split(" [").next().unwrap_or(value);
    if value.starts_with(&format!("{}:", snapshot.project)) {
        return snapshot.select(value);
    }
    let value = value.strip_prefix("./").unwrap_or(value);
    let invalid = || {
        format!(
            "code show: invalid selector {value:?}; use name, path:name, path:line:name, path::name or symbol id"
        )
    };
    if let Some((path, rest)) = snapshot
        .files
        .entries
        .iter()
        .filter_map(|f| {
            value
                .strip_prefix(&f.path)
                .and_then(|s| s.strip_prefix(':'))
                .map(|s| (f.path.as_str(), s))
        })
        .max_by_key(|(p, _)| p.len())
    {
        let (name, line) = if let Some(name) = rest.strip_prefix(':') {
            (name, None)
        } else if let Some((line, name)) = rest.split_once(':').filter(|(_, n)| !n.starts_with(':'))
        {
            let line = line
                .parse::<i64>()
                .ok()
                .filter(|n| *n > 0)
                .ok_or_else(invalid)?;
            (name, Some(line))
        } else {
            (rest, None)
        };
        if name.is_empty() {
            return Err(invalid());
        }
        return Ok(snapshot
            .definitions(name)?
            .rows
            .into_iter()
            .filter(|r| r.path == path && line.is_none_or(|n| r.range.line == n))
            .collect());
    }
    if value.is_empty() || value.replace("::", "").contains(':') {
        return Err(invalid());
    }
    snapshot.select(value)
}

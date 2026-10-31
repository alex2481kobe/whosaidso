// Budgeted presentation for symbol bodies and file structure.
mod outline;
use super::contracts::{GAP_LIMIT, ROW_BYTES};
const BODY_LINES: usize = 100;
const BODY_BYTES: usize = 16_384;
const SHOW_LIMIT: usize = 3;
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
pub(super) fn answer(
    snapshot: &Snapshot,
    command: &str,
    args: &[String],
) -> Result<String, String> {
    let (value, filter, offset, limit) = if command == "outline" {
        outline::parse(args)?
    } else {
        (args.join(" "), None, 0, SHOW_LIMIT)
    };
    let mut rows = if command == "show" {
        select(snapshot, &value)?
    } else {
        snapshot.outline(&value)?
    };
    if let Some(part) = filter {
        rows.retain(|row| row.qualified.contains(&part));
    }
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
            "ambiguous: {} definitions; use path:line:name or symbol id; bodies share 100 lines / 16384 bytes",
            rows.len()
        ));
    }
    let (mut lines_left, mut bytes_left) = (BODY_LINES, BODY_BYTES);
    for row in rows.iter().skip(offset).take(limit) {
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
        if command == "show" {
            match snapshot.symbol_body(row)? {
                Some((_, body)) => {
                    let text = String::from_utf8(body).map_err(|e| e.to_string())?;
                    let mut shown = String::new();
                    for line in text.lines().take(lines_left) {
                        if line.len() + 1 > bytes_left {
                            break;
                        }
                        shown.push_str(line);
                        shown.push('\n');
                        lines_left -= 1;
                        bytes_left -= line.len() + 1;
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
    let shown = rows.len().saturating_sub(offset).min(limit);
    if command == "outline" && (offset > 0 || shown < rows.len()) {
        out.push(format!(
            "shown {shown} of {} rows; offset {offset}; limit {limit}",
            rows.len()
        ));
    } else if rows.len() > limit {
        out.push(format!("shown {limit} of {}", rows.len()));
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
    if value.starts_with(&format!("{}:", snapshot.project)) {
        return snapshot.select(value);
    }
    let value = value.strip_prefix("./").unwrap_or(value);
    let invalid = || {
        format!(
            "code show: invalid selector {value:?}; use name, path:name, path:line, path:line:name, path::name or symbol id"
        )
    };
    if value.ends_with(" [label truncated]") {
        return Err(invalid());
    }
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
        let location = rest.split(" [").next().unwrap_or(rest);
        let (name, line) = if let Ok(line) = location.parse::<i64>() {
            if line <= 0 {
                return Err(invalid());
            }
            ("", Some(line))
        } else if let Some(name) = rest.strip_prefix(':') {
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
        if name.is_empty() && line.is_none() {
            return Err(invalid());
        }
        let mut name = name;
        loop {
            let rows: Vec<_> = snapshot
                .definitions(name)?
                .rows
                .into_iter()
                .filter(|r| r.path == path && line.is_none_or(|n| r.range.line == n))
                .collect();
            if !rows.is_empty() {
                return Ok(rows);
            }
            let Some((without, _)) = name.rsplit_once(" [").filter(|(_, tag)| tag.ends_with(']'))
            else {
                return Ok(rows);
            };
            name = without;
        }
    }
    if value.is_empty() || value.replace("::", "").contains(':') {
        return Err(invalid());
    }
    snapshot.select(value)
}

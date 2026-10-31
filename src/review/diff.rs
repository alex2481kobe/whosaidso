// Unified diff headers and hunks select scope; source snapshots provide the actual facts.
use super::changes::Change;
pub(super) fn parse(text: &str) -> Result<Vec<Change>, String> {
    let mut changes = Vec::new();
    let mut current = None;
    let mut recognized = false;
    let mut file_headers = false;
    let mut remaining = (0_i64, 0_i64);
    for line in text.lines() {
        if remaining != (0, 0) {
            match line.as_bytes().first() {
                Some(b'-') if remaining.0 > 0 => remaining.0 -= 1,
                Some(b'+') if remaining.1 > 0 => remaining.1 -= 1,
                Some(b' ') if remaining.0 > 0 && remaining.1 > 0 => {
                    remaining.0 -= 1;
                    remaining.1 -= 1;
                }
                Some(b'\\') => {}
                _ => return Err("diff hunk length does not match its header".into()),
            }
            continue;
        }
        if line.starts_with("diff --git ") {
            if let Some(change) = current.take() {
                changes.push(change);
            }
            current = Some(Change::default());
            file_headers = false;
            recognized = true;
        } else if line.starts_with("--- ") || line.starts_with("+++ ") {
            if line.starts_with("--- ") && file_headers {
                if let Some(change) = current.take() {
                    changes.push(change);
                }
                file_headers = false;
            }
            let change = current.get_or_insert_with(Change::default);
            let path = path(&line[4..], true)?;
            if line.starts_with("--- ") {
                change.old = path;
            } else {
                change.new = path;
                file_headers = true;
            }
            recognized = true;
        } else if let Some(name) = line.strip_prefix("rename from ") {
            current
                .as_mut()
                .ok_or("diff rename has no file header")?
                .old = path(name, false)?;
        } else if let Some(name) = line.strip_prefix("rename to ") {
            current
                .as_mut()
                .ok_or("diff rename has no file header")?
                .new = path(name, false)?;
        } else if line.starts_with("@@ ") {
            let change = current.as_mut().ok_or("diff hunk has no file header")?;
            let mut fields = line.split_whitespace();
            fields.next();
            change
                .old_ranges
                .push(range(fields.next().ok_or("invalid diff hunk")?, '-')?);
            change
                .new_ranges
                .push(range(fields.next().ok_or("invalid diff hunk")?, '+')?);
            remaining = (
                change.old_ranges.last().unwrap().1,
                change.new_ranges.last().unwrap().1,
            );
            if fields.next() != Some("@@") {
                return Err("invalid diff hunk".into());
            }
        }
    }
    if remaining != (0, 0) {
        return Err("incomplete diff hunk".into());
    }
    if let Some(change) = current {
        changes.push(change);
    }
    if !text.trim().is_empty() && !recognized {
        return Err("no supported diff headers".into());
    }
    if changes.iter().any(|c| c.old.is_none() && c.new.is_none()) {
        return Err(
            "diff has no readable file paths (binary or mode-only diff needs snapshots)".into(),
        );
    }
    Ok(changes)
}
fn range(value: &str, prefix: char) -> Result<(i64, i64), String> {
    let value = value.strip_prefix(prefix).ok_or("invalid diff range")?;
    let (start, count) = value.split_once(',').unwrap_or((value, "1"));
    let number = |s: &str| {
        s.parse::<i64>()
            .ok()
            .filter(|n| *n >= 0)
            .ok_or_else(|| "invalid diff range".to_string())
    };
    let start = number(start)?;
    let count = number(count)?;
    start
        .checked_add(count.max(1))
        .ok_or("diff range overflow")?;
    Ok((start, count))
}
fn path(value: &str, prefixed: bool) -> Result<Option<String>, String> {
    let value = value.split('\t').next().unwrap_or(value);
    if value == "/dev/null" {
        return Ok(None);
    }
    let value = if value.starts_with('"') {
        unquote(value)?
    } else {
        value.to_owned()
    };
    let path = if prefixed {
        value
            .strip_prefix("a/")
            .or_else(|| value.strip_prefix("b/"))
            .unwrap_or(&value)
    } else {
        &value
    };
    if path.is_empty()
        || std::path::Path::new(path).is_absolute()
        || std::path::Path::new(path)
            .components()
            .any(|c| matches!(c, std::path::Component::ParentDir))
    {
        return Err("invalid diff path".into());
    }
    Ok(Some(path.to_owned()))
}
fn unquote(value: &str) -> Result<String, String> {
    let inner = value
        .strip_prefix('"')
        .and_then(|s| s.strip_suffix('"'))
        .ok_or("invalid quoted diff path")?;
    let mut bytes = inner.bytes();
    let mut out = Vec::new();
    while let Some(b) = bytes.next() {
        if b != b'\\' {
            out.push(b);
            continue;
        }
        let b = bytes.next().ok_or("invalid diff escape")?;
        out.push(match b {
            b'n' => b'\n',
            b't' => b'\t',
            b'r' => b'\r',
            b'\\' | b'"' => b,
            b'0'..=b'3' => {
                let second = bytes
                    .next()
                    .filter(|b| (b'0'..=b'7').contains(b))
                    .ok_or("invalid diff octal")?;
                let third = bytes
                    .next()
                    .filter(|b| (b'0'..=b'7').contains(b))
                    .ok_or("invalid diff octal")?;
                (b - b'0') * 64 + (second - b'0') * 8 + third - b'0'
            }
            _ => return Err("unsupported diff escape".into()),
        });
    }
    String::from_utf8(out).map_err(|_| "diff path is not UTF-8".into())
}

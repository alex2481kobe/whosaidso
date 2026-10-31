// SCIP decoding, atomic import, and fresh precise answers.
use crate::{cache, tree};
use rusqlite::{Connection, TransactionBehavior, params};
use std::{
    collections::{HashMap, HashSet},
    fs,
    path::Path,
};

fn err(e: impl std::fmt::Display) -> String {
    e.to_string()
}
fn malformed() -> String {
    "malformed SCIP index".into()
}

// The wire format and field numbers follow the SCIP protobuf schema (scip.proto).
fn varint(data: &[u8], pos: &mut usize) -> Result<u64, String> {
    let mut value = 0u64;
    for i in 0..10 {
        let b = *data.get(*pos).ok_or_else(malformed)?;
        *pos += 1;
        if i == 9 && b > 1 {
            return Err(malformed());
        }
        value |= u64::from(b & 127) << (i * 7);
        if b & 128 == 0 {
            return Ok(value);
        }
    }
    Err(malformed())
}
enum Value<'a> {
    Int(u64),
    Bytes(&'a [u8]),
    Other,
}
fn fields(data: &[u8]) -> Result<Vec<(u64, Value<'_>)>, String> {
    let mut out = Vec::new();
    let mut p = 0;
    while p < data.len() {
        let tag = varint(data, &mut p)?;
        if tag >> 3 == 0 || tag >> 3 > 0x1fffffff {
            return Err(malformed());
        }
        let v = match tag & 7 {
            0 => Value::Int(varint(data, &mut p)?),
            2 => {
                let n = usize::try_from(varint(data, &mut p)?).map_err(err)?;
                let end = p
                    .checked_add(n)
                    .filter(|&x| x <= data.len())
                    .ok_or_else(malformed)?;
                let b = &data[p..end];
                p = end;
                Value::Bytes(b)
            }
            1 | 5 => {
                let n = if tag & 7 == 1 { 8 } else { 4 };
                p = p
                    .checked_add(n)
                    .filter(|&x| x <= data.len())
                    .ok_or_else(malformed)?;
                Value::Other
            }
            _ => return Err(malformed()),
        };
        out.push((tag >> 3, v));
    }
    Ok(out)
}
fn string(v: Value<'_>) -> Result<String, String> {
    match v {
        Value::Bytes(b) => Ok(std::str::from_utf8(b).map_err(|_| malformed())?.to_owned()),
        _ => Err(malformed()),
    }
}
fn bytes(v: Value<'_>) -> Result<&[u8], String> {
    if let Value::Bytes(b) = v {
        Ok(b)
    } else {
        Err(malformed())
    }
}
fn integer(v: Value<'_>) -> Result<u64, String> {
    if let Value::Int(n) = v {
        Ok(n)
    } else {
        Err(malformed())
    }
}

// SCIP's descriptor grammar: a backquoted name is one name, and a doubled backtick inside it is a literal one.
fn descriptors(symbol: &str) -> Vec<String> {
    let b = symbol.as_bytes();
    let (mut i, mut spaces) = (0, 0);
    while i < b.len() && spaces < 4 {
        if b[i] == b' ' {
            if i + 1 < b.len() && b[i + 1] == b' ' {
                i += 1;
            } else {
                spaces += 1;
            }
        }
        i += 1;
    }
    if spaces != 4 {
        return Vec::new();
    }
    let mut names = Vec::new();
    while i < b.len() {
        let part;
        if b[i] == b'(' || b[i] == b'[' {
            let end = if b[i] == b'(' { b')' } else { b']' };
            i += 1;
            let start = i;
            while i < b.len() && b[i] != end {
                i += 1;
            }
            part = symbol[start..i].to_string();
            i += usize::from(i < b.len());
        } else {
            let start = i;
            let mut quoted = false;
            while i < b.len() {
                if b[i] == b'`' {
                    if quoted && i + 1 < b.len() && b[i + 1] == b'`' {
                        i += 2;
                        continue;
                    }
                    quoted = !quoted;
                    i += 1;
                    continue;
                }
                if !quoted && b"/#.:!([".contains(&b[i]) {
                    break;
                }
                i += 1;
            }
            part = symbol[start..i]
                .replace("``", "\0")
                .replace('`', "")
                .replace('\0', "`");
            if i < b.len() && b[i] == b'(' {
                while i < b.len() && b[i] != b')' {
                    i += 1;
                }
                i += usize::from(i < b.len());
            }
            if i < b.len() {
                i += 1;
            }
        }
        if !part.is_empty() {
            names.push(part);
        }
    }
    names
}
fn name(symbol: &str) -> String {
    descriptors(symbol).pop().unwrap_or_default()
}
#[derive(Default)]
struct Doc {
    path: String,
    occ: Vec<Occ>,
    syms: Vec<(String, String)>,
    encoding: u64,
}
#[derive(PartialEq, Eq, PartialOrd, Ord)]
struct Occ {
    symbol: String,
    line: i64,
    end: i64,
    start: usize,
    stop: usize,
    def: bool,
}
fn occurrence(data: &[u8]) -> Result<Occ, String> {
    let (mut symbol, mut roles, mut range, mut typed) = (String::new(), 0, Vec::new(), None);
    for (n, v) in fields(data)? {
        match n {
            1 => match v {
                Value::Int(x) => range.push(x),
                Value::Bytes(b) => {
                    let mut p = 0;
                    while p < b.len() {
                        range.push(varint(b, &mut p)?);
                    }
                }
                _ => return Err(malformed()),
            },
            2 => symbol = string(v)?,
            3 => roles = integer(v)?,
            8 | 9 => {
                let mut r = [0; 4];
                for (field, v) in fields(bytes(v)?)? {
                    if (1..=if n == 8 { 3 } else { 4 }).contains(&field) {
                        r[field as usize - 1] = integer(v)?;
                    }
                }
                typed = Some(if n == 8 { [r[0], r[1], r[0], r[2]] } else { r });
            }
            _ => (),
        }
    }
    if !range.is_empty() && !matches!(range.len(), 3 | 4) {
        return Err(malformed());
    }
    let r = typed
        .or_else(|| {
            range.first().map(|&line| {
                if range.len() == 4 {
                    [line, range[1], range[2], range[3]]
                } else {
                    [line, range[1], line, range[2]]
                }
            })
        })
        .ok_or_else(malformed)?;
    if r.iter().chain(&range).any(|&n| n > i32::MAX as u64) {
        return Err(malformed());
    }
    Ok(Occ {
        symbol,
        line: r[0] as i64 + 1,
        end: r[2] as i64 + 1,
        start: r[1] as usize,
        stop: r[3] as usize,
        def: roles & 1 != 0,
    })
}
fn document(data: &[u8]) -> Result<Doc, String> {
    let mut d = Doc::default();
    for (n, v) in fields(data)? {
        match n {
            1 => d.path = string(v)?,
            2 => d.occ.push(occurrence(bytes(v)?)?),
            3 => {
                let (mut symbol, mut display) = (String::new(), String::new());
                for (n, v) in fields(bytes(v)?)? {
                    match n {
                        1 => symbol = string(v)?,
                        6 => display = string(v)?,
                        _ => (),
                    }
                }
                d.syms.push((symbol, display));
            }
            6 => d.encoding = integer(v)?,
            _ => (),
        }
    }
    if d.path.is_empty() {
        return Err(malformed());
    }
    Ok(d)
}
fn schema(db: &Connection) -> Result<(), String> {
    db.execute_batch("CREATE TABLE IF NOT EXISTS scip_docs(path TEXT PRIMARY KEY, blob TEXT); CREATE TABLE IF NOT EXISTS scip_occ(path TEXT, symbol TEXT, line INTEGER, is_def INTEGER); CREATE TABLE IF NOT EXISTS scip_syms(symbol TEXT PRIMARY KEY, name TEXT); CREATE INDEX IF NOT EXISTS scip_occ_symbol ON scip_occ(symbol); CREATE INDEX IF NOT EXISTS scip_syms_name ON scip_syms(name);").map_err(err)?;
    let tx =
        rusqlite::Transaction::new_unchecked(db, TransactionBehavior::Immediate).map_err(err)?;
    let has_name: i64 = tx
        .query_row(
            "SELECT count(*) FROM pragma_table_info('scip_syms') WHERE name='descriptor_name'",
            [],
            |r| r.get(0),
        )
        .map_err(err)?;
    if has_name == 0 {
        tx.execute_batch("ALTER TABLE scip_syms ADD COLUMN descriptor_name TEXT;")
            .map_err(err)?;
        let symbols = tx
            .prepare("SELECT symbol FROM scip_syms")
            .map_err(err)?
            .query_map([], |r| r.get::<_, String>(0))
            .map_err(err)?
            .collect::<Result<Vec<_>, _>>()
            .map_err(err)?;
        for symbol in symbols {
            tx.execute(
                "UPDATE scip_syms SET descriptor_name=?2 WHERE symbol=?1",
                params![symbol, name(&symbol)],
            )
            .map_err(err)?;
        }
    }
    tx.execute_batch(
        "CREATE INDEX IF NOT EXISTS scip_descriptor_name ON scip_syms(descriptor_name);",
    )
    .map_err(err)?;
    tx.commit().map_err(err)
}
fn symbol_key(path: &str, symbol: &str) -> String {
    if symbol.starts_with("local ") {
        format!("{path}\0{symbol}")
    } else {
        symbol.to_owned()
    }
}
// An index may list one path in several documents; they describe one file, so merge them and drop repeats.
fn merge_paths(docs: Vec<Doc>) -> Result<Vec<Doc>, String> {
    let mut merged: Vec<Doc> = Vec::new();
    let mut at = HashMap::new();
    for (document, mut d) in docs.into_iter().enumerate() {
        // Local IDs belong to the original document, even when paths repeat.
        for o in &mut d.occ {
            if o.symbol.starts_with("local ") {
                o.symbol = format!("local {document} {}", o.symbol);
            }
        }
        for (symbol, _) in &mut d.syms {
            if symbol.starts_with("local ") {
                *symbol = format!("local {document} {symbol}");
            }
        }
        match at.get(&d.path) {
            Some(&i) => {
                let m: &mut Doc = &mut merged[i];
                if m.encoding != d.encoding {
                    return Err(malformed());
                }
                m.occ.extend(d.occ);
                m.syms.extend(d.syms);
            }
            None => {
                at.insert(d.path.clone(), merged.len());
                merged.push(d);
            }
        }
    }
    for d in &mut merged {
        d.occ.sort();
        d.occ.dedup();
        d.syms.sort();
        d.syms.dedup();
    }
    Ok(merged)
}
fn identifier(value: &str) -> bool {
    let mut chars = value.chars();
    chars.next().is_some_and(|c| c == '_' || c.is_alphabetic())
        && chars.all(|c| c == '_' || c.is_alphanumeric())
}
fn unchecked_reference(symbol: &str, expected: &str) -> bool {
    if symbol.ends_with('#') || expected.starts_with("__") {
        return true;
    }
    let Some((owner, method)) = symbol
        .strip_suffix(").")
        .and_then(|s| s.rsplit_once('('))
        .and_then(|(s, _)| s.rsplit_once('#'))
    else {
        return false;
    };
    // Implicit constructor ranges select arguments, not the enclosing type's name.
    identifier(method.trim_matches('`')) && name(&format!("{owner}#")) == expected
}
fn offset(line: &[u8], column: usize, encoding: u64) -> Option<usize> {
    // scip-clang leaves encoding unspecified and emits byte columns.
    if encoding <= 1 {
        return (column <= line.len()).then_some(column);
    }
    if encoding > 3 {
        return None;
    }
    let mut units = 0;
    for (byte, c) in std::str::from_utf8(line).ok()?.char_indices() {
        if units == column {
            return Some(byte);
        }
        units += if encoding == 2 { c.len_utf16() } else { 1 };
    }
    (units == column).then_some(line.len())
}
fn check(d: &Doc, bytes: &[u8]) -> bool {
    let mut lines: Vec<_> = bytes.split(|&b| b == b'\n').collect();
    if bytes.is_empty() || bytes.ends_with(b"\n") {
        lines.pop();
    }
    let locals: HashMap<_, _> = d
        .syms
        .iter()
        .map(|(s, n)| (s.as_str(), n.as_str()))
        .collect();
    let range = |o: &Occ| (o.line, o.end, o.start, o.stop);
    // Macro expansions may define identifiers whose spelling is absent from the source.
    let macros: HashSet<_> = d
        .occ
        .iter()
        .filter(|o| o.symbol.ends_with('!'))
        .map(range)
        .collect();
    let (mut valid, mut checked) = (true, 0);
    for o in &d.occ {
        valid &= o.line <= lines.len() as i64 && o.end <= lines.len() as i64;
        let expected = if o.symbol.starts_with("local ") {
            locals
                .get(o.symbol.as_str())
                .copied()
                .unwrap_or("")
                .to_owned()
        } else {
            name(&o.symbol)
        };
        if !identifier(&expected) || macros.contains(&range(o)) {
            continue;
        }
        if !o.def && unchecked_reference(&o.symbol, &expected) {
            continue;
        }
        checked += 1;
        let selected = lines.get(o.line as usize - 1).and_then(|line| {
            line.get(offset(line, o.start, d.encoding)?..offset(line, o.stop, d.encoding)?)
        });
        let matches = o.line == o.end && selected == Some(expected.as_bytes());
        valid &= matches;
    }
    valid && checked > 0
}
pub fn import(root: &Path, file: &Path) -> Result<(), String> {
    let data = fs::read(file).map_err(|e| format!("{}: {e}", file.display()))?;
    let mut docs = Vec::new();
    let mut metadata = false;
    for (n, v) in fields(&data)? {
        match n {
            1 => {
                if metadata || !docs.is_empty() {
                    return Err(malformed());
                }
                let _ = fields(bytes(v)?)?;
                metadata = true;
            }
            2 => {
                if !metadata {
                    return Err(malformed());
                }
                docs.push(document(bytes(v)?)?);
            }
            _ => (),
        }
    }
    if !metadata {
        return Err(malformed());
    }
    let docs = merge_paths(docs)?;
    let files = tree::files(root)?;
    let paths: HashSet<_> = docs.iter().map(|d| d.path.as_str()).collect();
    let mut blobs = tree::blobs(&files);
    let sources: Vec<_> = files
        .entries
        .into_iter()
        .filter(|f| paths.contains(f.path.as_str()))
        .collect();
    let by_path: HashMap<_, _> = docs.iter().map(|d| (d.path.as_str(), d)).collect();
    tree::read(root, &sources, usize::MAX - 1, |file, bytes| {
        blobs.remove(&file.path);
        if let Some(bytes) = bytes {
            let d = by_path[file.path.as_str()];
            if check(d, &bytes) {
                blobs.insert(file.path.clone(), file.blob.clone());
            }
        }
        Ok(())
    })?;
    let mut db = cache::db(root)?;
    schema(&db)?;
    let tx = db
        .transaction_with_behavior(TransactionBehavior::Immediate)
        .map_err(err)?;
    // Rebuild secondary indexes once, rather than maintaining them for millions of imported rows.
    tx.execute_batch(
        "DROP INDEX scip_occ_symbol; DROP INDEX scip_syms_name; DROP INDEX scip_descriptor_name;
        DELETE FROM scip_docs; DELETE FROM scip_occ; DELETE FROM scip_syms;",
    )
    .map_err(err)?;
    let mut symbols = HashMap::new();
    let mut count = 0;
    let mut stale = 0;
    let mut insert_doc = tx
        .prepare("INSERT INTO scip_docs VALUES (?1,?2)")
        .map_err(err)?;
    let mut insert_occ = tx
        .prepare("INSERT INTO scip_occ VALUES (?1,?2,?3,?4)")
        .map_err(err)?;
    for d in &docs {
        let blob = blobs.get(&d.path);
        if blob.is_none() {
            stale += 1;
        }
        insert_doc.execute(params![d.path, blob]).map_err(err)?;
        for Occ {
            symbol, line, def, ..
        } in &d.occ
        {
            let key = symbol_key(&d.path, symbol);
            insert_occ
                .execute(params![d.path, key, line, def])
                .map_err(err)?;
            count += 1;
            if !symbol.is_empty() {
                symbols.entry(key).or_insert_with(|| name(symbol));
            }
        }
        for (symbol, display) in &d.syms {
            if !symbol.is_empty() {
                let entry = symbols
                    .entry(symbol_key(&d.path, symbol))
                    .or_insert_with(|| name(symbol));
                if !display.is_empty() {
                    *entry = display.clone();
                }
            }
        }
    }
    let mut insert_sym = tx
        .prepare("INSERT INTO scip_syms VALUES (?1,?2,?3)")
        .map_err(err)?;
    for (symbol, name) in &symbols {
        insert_sym
            .execute(params![
                symbol,
                name,
                descriptors(symbol).pop().unwrap_or_default()
            ])
            .map_err(err)?;
    }
    drop((insert_doc, insert_occ, insert_sym));
    tx.execute_batch("CREATE INDEX scip_occ_symbol ON scip_occ(symbol); CREATE INDEX scip_syms_name ON scip_syms(name); CREATE INDEX scip_descriptor_name ON scip_syms(descriptor_name);").map_err(err)?;
    tx.commit().map_err(err)?;
    println!(
        "scip: {} documents, {count} occurrences, {} symbols, {stale} stale documents",
        docs.len(),
        symbols.len()
    );
    Ok(())
}
pub fn answers(
    db: &Connection,
    files: &tree::Files,
    value: &str,
    def: bool,
    answers: &mut super::query::Answers,
) -> Result<HashSet<String>, String> {
    schema(db)?;
    let qualified = super::query::parts(value).len() > 1;
    let leaf = super::query::leaf(value);
    let column = if qualified {
        "s.descriptor_name"
    } else {
        "s.name"
    };
    // Only matching precise and syntactic rows can affect this answer's freshness.
    let syntax = if def {
        "SELECT path FROM defs WHERE name=?1"
    } else {
        "SELECT path FROM calls WHERE callee=?1"
    };
    let sql = format!(
        "SELECT path,blob FROM scip_docs WHERE path IN ({syntax} UNION SELECT o.path FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE {column}=?1 AND o.is_def=?2)"
    );
    let mut stmt = db.prepare(&sql).map_err(err)?;
    let blobs: HashMap<String, Option<String>> = stmt
        .query_map(params![leaf, def], |r| Ok((r.get(0)?, r.get(1)?)))
        .map_err(err)?
        .collect::<Result<_, _>>()
        .map_err(err)?;
    let fresh: HashSet<String> = files
        .entries
        .iter()
        .filter(|f| {
            blobs
                .get(&f.path)
                .is_some_and(|b| b.as_ref() == Some(&f.blob))
        })
        .map(|f| f.path.clone())
        .collect();
    let mut stmt = db.prepare(&format!("SELECT o.path,o.line,o.symbol FROM scip_occ o JOIN scip_syms s ON s.symbol=o.symbol WHERE {column}=?1 AND o.is_def=?2 ORDER BY o.path,o.line")).map_err(err)?;
    let rows = stmt
        .query_map(params![leaf, def], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, i64>(1)?,
                r.get::<_, String>(2)?,
            ))
        })
        .map_err(err)?;
    for row in rows {
        let (path, line, symbol) = row.map_err(err)?;
        if fresh.contains(&path)
            && (!qualified
                || descriptors(&symbol)
                    .iter()
                    .map(String::as_str)
                    .collect::<Vec<_>>()
                    .ends_with(&super::query::parts(value)))
        {
            answers.push(|| format!("{path}:{line}  [precise]"));
        }
    }
    Ok(fresh)
}

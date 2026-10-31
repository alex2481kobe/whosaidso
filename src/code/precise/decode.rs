// SCIP protobuf decoding and document merging.
use super::wire::*;
use std::collections::HashMap;
#[derive(Default)]
pub(super) struct Doc {
    pub path: String,
    pub occ: Vec<Occ>,
    pub syms: Vec<Info>,
    pub encoding: u64,
}
#[derive(Default, PartialEq, Eq, PartialOrd, Ord)]
pub(super) struct Info {
    pub symbol: String,
    pub display: String,
    pub documentation: Vec<String>,
    pub relationships: Vec<Relationship>,
    pub enclosing: String,
}
#[derive(Default, PartialEq, Eq, PartialOrd, Ord)]
pub(super) struct Relationship {
    pub symbol: String,
    pub reference: bool,
    pub implementation: bool,
    pub type_definition: bool,
    pub definition: bool,
}
#[derive(PartialEq, Eq, PartialOrd, Ord)]
pub(super) struct Occ {
    pub symbol: String,
    pub line: i64,
    pub end: i64,
    pub start: usize,
    pub stop: usize,
    pub def: bool,
    pub enclosing: Option<[i64; 4]>,
    pub documentation: Vec<String>,
}
fn occurrence(data: &[u8]) -> Result<Occ, String> {
    let (mut enclosed, mut enclosing, mut documentation) = (Vec::new(), None, Vec::new());
    let (mut symbol, mut roles, mut range, mut typed) = (String::new(), 0, Vec::new(), None);
    for (n, v) in fields(data)? {
        match n {
            1 | 7 => {
                let range = if n == 1 { &mut range } else { &mut enclosed };
                match v {
                    Value::Int(x) => range.push(x),
                    Value::Bytes(b) => {
                        let mut p = 0;
                        while p < b.len() {
                            range.push(varint(b, &mut p)?);
                        }
                    }
                    _ => return Err(malformed()),
                }
            }
            2 => symbol = string(v)?,
            3 => roles = integer(v)?,
            4 => documentation.push(string(v)?),
            8..=11 => {
                let mut r = [0; 4];
                for (field, v) in fields(bytes(v)?)? {
                    if (1..=if n == 8 || n == 10 { 3 } else { 4 }).contains(&field) {
                        r[field as usize - 1] = integer(v)?;
                    }
                }
                let value = Some(if n == 8 || n == 10 {
                    [r[0], r[1], r[0], r[2]]
                } else {
                    r
                });
                if n < 10 {
                    typed = value;
                } else {
                    enclosing = value;
                }
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
    let enclosing = enclosing.or(match enclosed.as_slice() {
        [l, s, e] => Some([*l, *s, *l, *e]),
        [l, s, el, e] => Some([*l, *s, *el, *e]),
        _ => None,
    });
    if enclosing.is_none() && !enclosed.is_empty() {
        return Err(malformed());
    }
    if let Some(e) = enclosing
        && (e.iter().any(|&n| n > i32::MAX as u64)
            || (e[0], e[1]) > (r[0], r[1])
            || (e[2], e[3]) < (r[2], r[3]))
    {
        return Err(malformed());
    }
    Ok(Occ {
        symbol,
        line: r[0] as i64 + 1,
        end: r[2] as i64 + 1,
        start: r[1] as usize,
        stop: r[3] as usize,
        def: roles & 1 != 0,
        enclosing: enclosing.map(|e| [e[0] as i64 + 1, e[1] as i64, e[2] as i64 + 1, e[3] as i64]),
        documentation,
    })
}
pub(super) fn document(data: &[u8]) -> Result<Doc, String> {
    let mut d = Doc::default();
    for (n, v) in fields(data)? {
        match n {
            1 => d.path = string(v)?,
            2 => d.occ.push(occurrence(bytes(v)?)?),
            3 => {
                d.syms.push(information(bytes(v)?)?);
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

// An index may list one path in several documents; they describe one file, so merge them and drop repeats.
pub(super) fn merge_paths(docs: Vec<Doc>) -> Result<Vec<Doc>, String> {
    let mut merged: Vec<Doc> = Vec::new();
    let mut at = HashMap::new();
    for (document, mut d) in docs.into_iter().enumerate() {
        // Local IDs belong to the original document, even when paths repeat.
        for o in &mut d.occ {
            if o.symbol.starts_with("local ") {
                o.symbol = format!("local {document} {}", o.symbol);
            }
        }
        let local = |symbol: &mut String| {
            if symbol.starts_with("local ") {
                *symbol = format!("local {document} {symbol}");
            }
        };
        for info in &mut d.syms {
            local(&mut info.symbol);
            local(&mut info.enclosing);
            for relationship in &mut info.relationships {
                local(&mut relationship.symbol);
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

pub(super) fn information(data: &[u8]) -> Result<Info, String> {
    let mut info = Info::default();
    for (n, v) in fields(data)? {
        match n {
            1 => info.symbol = string(v)?,
            3 => info.documentation.push(string(v)?),
            4 => {
                let mut r = Relationship::default();
                for (n, v) in fields(bytes(v)?)? {
                    match n {
                        1 => r.symbol = string(v)?,
                        2 => r.reference = integer(v)? != 0,
                        3 => r.implementation = integer(v)? != 0,
                        4 => r.type_definition = integer(v)? != 0,
                        5 => r.definition = integer(v)? != 0,
                        _ => (),
                    }
                }
                info.relationships.push(r);
            }
            6 => info.display = string(v)?,
            8 => info.enclosing = string(v)?,
            _ => (),
        }
    }
    Ok(info)
}

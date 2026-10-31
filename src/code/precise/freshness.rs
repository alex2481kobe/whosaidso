// Validate occurrence spelling against the indexed source.
use super::{
    decode::{Doc, Occ},
    symbols::name,
};
use std::collections::{HashMap, HashSet};
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
fn aliases<'a>(d: &'a Doc, bytes: &[u8], lines: &[&[u8]]) -> HashMap<&'a str, HashSet<String>> {
    let mut spellings = HashMap::new();
    if !d.path.ends_with(".rs") || !d.occ.iter().any(|o| o.symbol.starts_with("rust-analyzer ")) {
        return spellings;
    }
    let Ok(aliases) = super::super::parse::names::rust_aliases(bytes) else {
        return spellings;
    };
    let at = |o: &Occ, r: tree_sitter::Range| {
        o.line == r.start_point.row as i64 + 1
            && o.end == r.end_point.row as i64 + 1
            && lines.get(o.line as usize - 1).is_some_and(|line| {
                offset(line, o.start, d.encoding) == Some(r.start_point.column)
                    && offset(line, o.stop, d.encoding) == Some(r.end_point.column)
            })
    };
    for alias in aliases {
        let Some(source) = alias.source else { continue };
        // The original spelling and both import ranges must identify the same SCIP symbol.
        for o in d.occ.iter().filter(|o| !o.def && at(o, source)) {
            if bytes.get(source.start_byte..source.end_byte) == Some(name(&o.symbol).as_bytes())
                && d.occ
                    .iter()
                    .any(|a| !a.def && a.symbol == o.symbol && at(a, alias.range))
            {
                spellings
                    .entry(o.symbol.as_str())
                    .or_insert_with(HashSet::new)
                    .insert(alias.name.clone());
            }
        }
    }
    spellings
}
pub(super) fn check(d: &Doc, bytes: &[u8]) -> bool {
    let source_lines: Vec<_> = bytes.split(|&b| b == b'\n').collect();
    let enclosing_valid = d.occ.iter().all(|o| {
        o.enclosing.is_none_or(|e| {
            let valid = |line: i64, column: i64| {
                source_lines
                    .get(line as usize - 1)
                    .and_then(|l| offset(l, column as usize, d.encoding))
                    .is_some()
            };
            valid(e[0], e[1]) && valid(e[2], e[3])
        })
    });
    let mut lines: Vec<_> = bytes.split(|&b| b == b'\n').collect();
    if bytes.is_empty() || bytes.ends_with(b"\n") {
        lines.pop();
    }
    let locals: HashMap<_, _> = d
        .syms
        .iter()
        .map(|s| (s.symbol.as_str(), s.display.as_str()))
        .collect();
    let range = |o: &Occ| (o.line, o.end, o.start, o.stop);
    // Macro expansions may define identifiers whose spelling is absent from the source.
    let macros: HashSet<_> = d
        .occ
        .iter()
        .filter(|o| o.symbol.ends_with('!'))
        .map(range)
        .collect();
    let aliases = aliases(d, bytes, &source_lines);
    let (mut valid, mut checked) = (enclosing_valid, 0);
    for o in &d.occ {
        let in_source = |line: i64, column: usize| {
            source_lines
                .get(line as usize - 1)
                .and_then(|l| offset(l, column, d.encoding))
                .is_some()
        };
        valid &= in_source(o.line, o.start) && in_source(o.end, o.stop);
        let expected = if o.symbol.starts_with("local ") {
            locals
                .get(o.symbol.as_str())
                .copied()
                .unwrap_or("")
                .to_owned()
        } else {
            name(&o.symbol)
        };
        // Rust module definitions select the entire file, including its final empty line.
        if o.symbol.ends_with('/') || !identifier(&expected) || macros.contains(&range(o)) {
            continue;
        }
        if !o.def && unchecked_reference(&o.symbol, &expected) {
            continue;
        }
        checked += 1;
        let selected = lines.get(o.line as usize - 1).and_then(|line| {
            line.get(offset(line, o.start, d.encoding)?..offset(line, o.stop, d.encoding)?)
        });
        // Implicit Rust trait calls do not spell the method name at their source range.
        let implicit = !o.def
            && o.symbol.starts_with("rust-analyzer ")
            && (rust_operator(&expected)
                || (o.symbol.contains("/impl#") && selected == Some(b"Self")));
        let alias = !o.def
            && aliases
                .get(o.symbol.as_str())
                .is_some_and(|names| names.iter().any(|name| selected == Some(name.as_bytes())));
        let matches =
            (o.line == o.end && (selected == Some(expected.as_bytes()) || alias)) || implicit;
        valid &= matches;
    }
    valid && checked > 0
}

// These Rust trait calls select operators, punctuation, or multiline expressions.
fn rust_operator(method: &str) -> bool {
    matches!(
        method,
        "eq" | "ne"
            | "lt"
            | "le"
            | "gt"
            | "ge"
            | "add"
            | "add_assign"
            | "sub"
            | "sub_assign"
            | "neg"
            | "mul"
            | "mul_assign"
            | "div"
            | "div_assign"
            | "rem"
            | "rem_assign"
            | "bitand"
            | "bitand_assign"
            | "bitor"
            | "bitor_assign"
            | "bitxor"
            | "bitxor_assign"
            | "shl"
            | "shl_assign"
            | "shr"
            | "shr_assign"
            | "not"
            | "deref"
            | "deref_mut"
            | "index"
            | "index_mut"
            | "branch"
    )
}

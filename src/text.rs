// Text-search command boundary.
mod attributes;
mod git;
mod index;
mod matcher;
mod options;
mod parallel;
mod scan;
mod search;
use std::{
    io::{BufWriter, Write},
    path::Path,
};

pub(crate) fn prefix(text: &str, bytes: usize) -> &str {
    let mut end = bytes.min(text.len());
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    &text[..end]
}

pub fn cut(line: &str, limit: usize) -> String {
    if line.chars().count() <= limit {
        return line.to_owned();
    }
    format!(
        "{}...",
        line.chars()
            .take(limit.saturating_sub(3))
            .collect::<String>()
    )
}
pub fn grep(root: &Path, args: &[String]) -> Result<(), String> {
    let stdout = std::io::stdout();
    let mut out = BufWriter::new(stdout.lock());
    let options = options::parse(args)?;
    if options.regex && !options.files {
        eprintln!("grep: Git extended regex (--regex); | is alternation");
    }
    let (rows, total) = search::run(root, &options)?;
    if total == 0 {
        writeln!(out, "grep: no matches").map_err(|e| e.to_string())?;
        if !options.regex
            && !options.files
            && options
                .pattern
                .starts_with(|c: char| c.is_ascii_alphabetic() || c == '_')
            && options
                .pattern
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_')
        {
            eprintln!(
                "hint: try whosaidso find {0} or whosaidso code defs {0}",
                options.pattern
            );
        }
        return out.flush().map_err(|e| e.to_string());
    }
    let shown = total.saturating_sub(options.offset).min(options.limit);
    for row in rows.iter().skip(options.offset).take(options.limit) {
        writeln!(out, "{row}").map_err(|e| e.to_string())?;
    }
    if options.offset > 0 || shown < total {
        writeln!(
            out,
            "shown {shown} of {total} rows; offset {}; limit {}",
            options.offset, options.limit
        )
        .map_err(|e| e.to_string())?;
    }
    out.flush().map_err(|e| e.to_string())
}

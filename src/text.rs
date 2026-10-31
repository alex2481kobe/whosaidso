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
    let (rows, total) = search::run(root, &options)?;
    if total == 0 {
        writeln!(out, "grep: no matches").map_err(|e| e.to_string())?;
        return out.flush().map_err(|e| e.to_string());
    }
    let shown = total.saturating_sub(options.offset).min(options.limit);
    for row in rows.iter().skip(options.offset).take(options.limit) {
        writeln!(out, "{row}").map_err(|e| e.to_string())?;
    }
    if options.offset > 0 || shown < total {
        writeln!(
            out,
            "shown {shown} of {total} rows; offset {}",
            options.offset
        )
        .map_err(|e| e.to_string())?;
    }
    out.flush().map_err(|e| e.to_string())
}

// Text-search command boundary.
mod index;
mod options;
mod search;
use std::path::Path;

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
    let options = options::parse(args)?;
    let rows = search::run(root, &options)?;
    let total = rows.len();
    if total == 0 {
        println!("grep: no matches");
        return Ok(());
    }
    let shown = total.saturating_sub(options.offset).min(options.limit);
    for row in rows.iter().skip(options.offset).take(options.limit) {
        println!("{row}");
    }
    if options.offset > 0 || shown < total {
        println!("shown {shown} of {total} rows; offset {}", options.offset);
    }
    Ok(())
}

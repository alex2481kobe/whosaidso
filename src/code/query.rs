// Read and format code answers.
use super::{contracts::Answers, facts::reads::Layer};
use std::path::Path;

pub fn run(root: &Path, command: &str, value: &str) -> Result<(), String> {
    let snapshot = super::facts::Snapshot::current(root)?;
    if ["deps", "includers"].contains(&command) && super::languages::for_path(value).is_none() {
        println!("code: {value}: not indexed (unsupported language)");
        return Ok(());
    }
    let mut answers = Answers::default();
    match command {
        "defs" | "refs" => {
            let result = if command == "defs" {
                snapshot.definitions(value)?
            } else {
                snapshot.references(value)?
            };
            for row in result.rows {
                let path = row.path;
                let line = row.range.line;
                match row.layer {
                    Layer::Precise => answers.push(|| {
                        let container = if row.caller.is_empty() {
                            String::new()
                        } else {
                            format!(" in {}", row.caller)
                        };
                        format!("{path}:{line}  [precise]{container}")
                    }),
                    Layer::Syntactic if command == "defs" => answers
                        .push(|| format!("{path}:{line}  {} {}  [syntactic]", row.kind, row.name)),
                    Layer::Syntactic => {
                        let caller = if row.caller.is_empty() {
                            "<top level>"
                        } else {
                            &row.caller
                        };
                        answers.push(|| format!("{path}:{line}  in {caller}  [syntactic]"));
                    }
                }
            }
        }
        "deps" => {
            let rows = snapshot.dependencies(value)?;
            for row in rows {
                let line = row;
                answers.push(|| line);
            }
        }
        "includers" => {
            for line in snapshot.includers(value)? {
                answers.push(|| line);
            }
        }
        _ => return Err("unknown code query".into()),
    }
    let coverage = snapshot.coverage()?;
    if answers.total == 0 {
        for c in coverage
            .iter()
            .filter(|c| c.syntax == "not indexed" && c.precise != "fresh")
            .take(5)
        {
            println!("coverage: {}: not indexed", c.path);
        }
    }
    if answers.total == 0
        && !coverage.is_empty()
        && !coverage
            .iter()
            .any(|c| c.syntax == "indexed" || c.precise == "fresh")
    {
        println!("code: not indexed; no searchable code coverage");
    } else if answers.total == 0 {
        println!("code: none recorded in indexed files; absence is not proven [hybrid]");
    } else {
        for line in &answers.lines {
            println!("{line}");
        }
        if answers.total > 20 {
            println!("shown 20 of {}", answers.total);
        }
    }
    Ok(())
}

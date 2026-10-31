// Read and format code answers.
use super::{contracts::Answers, facts::reads::Layer, store};
use std::path::Path;

pub fn run(root: &Path, command: &str, value: &str) -> Result<(), String> {
    let snapshot = super::facts::Snapshot::current(root)?;
    let db = &snapshot.db;
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
            let rows = store::reads::dependencies(db, value)?;
            for row in rows {
                let line = row;
                answers.push(|| line);
            }
        }
        "includers" => {
            let name = Path::new(value)
                .file_name()
                .and_then(|s| s.to_str())
                .unwrap_or("");
            let rows = store::reads::includers(db)?;
            for row in rows {
                let (path, line, target) = row;
                if !name.is_empty()
                    && (target == name
                        || target
                            .strip_suffix(name)
                            .is_some_and(|prefix| prefix.ends_with('/')))
                {
                    answers.push(|| format!("included by {path}:{line}  [syntactic, by name]"));
                }
            }
        }
        _ => return Err("unknown code query".into()),
    }
    if answers.total == 0 {
        println!("code: none recorded [syntactic]");
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

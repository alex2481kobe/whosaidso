// Read and format code answers.
use super::store;
use std::path::Path;

#[derive(Default)]
pub struct Answers {
    lines: Vec<String>,
    total: usize,
}
impl Answers {
    pub fn push(&mut self, line: impl FnOnce() -> String) {
        self.total += 1;
        if self.lines.len() < 20 {
            self.lines.push(crate::text::cut(&line(), 200));
        }
    }
}
pub fn parts(value: &str) -> Vec<&str> {
    value.split("::").flat_map(|s| s.split('.')).collect()
}
fn matches(qualified: &str, value: &str) -> bool {
    parts(qualified).ends_with(&parts(value))
}
pub fn leaf(value: &str) -> &str {
    value
        .rsplit("::")
        .next()
        .unwrap_or(value)
        .rsplit('.')
        .next()
        .unwrap_or(value)
}
pub fn run(
    root: &Path,
    command: &str,
    value: &str,
    files: &crate::tree::Files,
) -> Result<(), String> {
    let db = store::open(root)?;
    let mut answers = Answers::default();
    match command {
        "defs" => {
            let fresh = super::precise::answers(&db, files, value, true, &mut answers)?;
            let mut stmt = db.prepare("SELECT path, line, kind, name, qualified FROM defs WHERE name = ?1 ORDER BY path, line").map_err(|e| e.to_string())?;
            let rows = stmt
                .query_map([leaf(value)], |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, i64>(1)?,
                        r.get::<_, String>(2)?,
                        r.get::<_, String>(3)?,
                        r.get::<_, String>(4)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            for row in rows {
                let (path, line, kind, name, qualified) = row.map_err(|e| e.to_string())?;
                if !fresh.contains(&path) && matches(&qualified, value) {
                    answers.push(|| format!("{path}:{line}  {kind} {name}  [syntactic]"));
                }
            }
        }
        "refs" => {
            let fresh = super::precise::answers(&db, files, value, false, &mut answers)?;
            let mut stmt = db.prepare("SELECT path, line, caller, qualified FROM calls WHERE callee = ?1 ORDER BY path, line").map_err(|e| e.to_string())?;
            let rows = stmt
                .query_map([leaf(value)], |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, i64>(1)?,
                        r.get::<_, String>(2)?,
                        r.get::<_, String>(3)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            for row in rows {
                let (path, line, caller, qualified) = row.map_err(|e| e.to_string())?;
                if !fresh.contains(&path) && matches(&qualified, value) {
                    let caller = if caller.is_empty() {
                        "<top level>"
                    } else {
                        caller.as_str()
                    };
                    answers.push(|| format!("{path}:{line}  in {caller}  [syntactic]"));
                }
            }
        }
        "deps" => {
            let mut stmt = db
                .prepare("SELECT target, line FROM includes WHERE path = ?1 ORDER BY line, target")
                .map_err(|e| e.to_string())?;
            let rows = stmt
                .query_map([value], |r| {
                    Ok(format!(
                        "{}:{}  includes {}  [syntactic, by name]",
                        value,
                        r.get::<_, i64>(1)?,
                        r.get::<_, String>(0)?
                    ))
                })
                .map_err(|e| e.to_string())?;
            for row in rows {
                let line = row.map_err(|e| e.to_string())?;
                answers.push(|| line);
            }
        }
        "includers" => {
            let name = Path::new(value)
                .file_name()
                .and_then(|s| s.to_str())
                .unwrap_or("");
            let mut stmt = db
                .prepare("SELECT path, line, target FROM includes ORDER BY path, line")
                .map_err(|e| e.to_string())?;
            let rows = stmt
                .query_map([], |r| {
                    Ok((
                        r.get::<_, String>(0)?,
                        r.get::<_, i64>(1)?,
                        r.get::<_, String>(2)?,
                    ))
                })
                .map_err(|e| e.to_string())?;
            for row in rows {
                let (path, line, target) = row.map_err(|e| e.to_string())?;
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

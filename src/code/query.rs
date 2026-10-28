// Read and format syntactic graph answers.
use super::store;
use std::path::Path;

pub fn parts(value: &str) -> Vec<&str> { value.split("::").flat_map(|s| s.split('.')).collect() }
pub fn matches(qualified: &str, value: &str) -> bool {
    parts(qualified).ends_with(&parts(value))
}
pub fn leaf(value: &str) -> &str { value.rsplit("::").next().unwrap_or(value).rsplit('.').next().unwrap_or(value) }
pub fn run(root: &Path, command: &str, value: &str, files: &crate::tree::Files) -> Result<(), String> {
    let db = store::open(root)?;
    let mut lines = Vec::new();
    let mut omitted = 0;
    match command {
        "defs" => {
            let (fresh, precise) = super::precise::answers(&db, files, value, true)?;
            lines.extend(precise);
            let mut stmt = db.prepare("SELECT path, line, kind, name, qualified FROM defs WHERE name = ?1 ORDER BY path, line").map_err(|e| e.to_string())?;
            let rows = stmt.query_map([leaf(value)], |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?, r.get::<_, String>(2)?, r.get::<_, String>(3)?, r.get::<_, String>(4)?))).map_err(|e| e.to_string())?;
            for row in rows { let (path, line, kind, name, qualified) = row.map_err(|e| e.to_string())?; if !fresh.contains(&path) && matches(&qualified, value) { lines.push(format!("{path}:{line}  {kind} {name}  [syntactic]")); } }
        }
        "refs" => {
            let (fresh, precise) = super::precise::answers(&db, files, value, false)?;
            lines.extend(precise);
            let mut stmt = db.prepare("SELECT path, line, caller, qualified FROM calls WHERE callee = ?1 ORDER BY path, line").map_err(|e| e.to_string())?;
            let rows = stmt.query_map([leaf(value)], |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?, r.get::<_, String>(2)?, r.get::<_, String>(3)?))).map_err(|e| e.to_string())?;
            for row in rows { let (path, line, caller, qualified) = row.map_err(|e| e.to_string())?; if !fresh.contains(&path) && matches(&qualified, value) { let caller = if caller.is_empty() { "<top level>" } else { caller.as_str() }; lines.push(format!("{path}:{line}  in {caller}  [syntactic]")); } }
        }
        "deps" => {
            let mut stmt = db.prepare("SELECT target, line FROM includes WHERE path = ?1 ORDER BY line, target").map_err(|e| e.to_string())?;
            let rows = stmt.query_map([value], |r| Ok(format!("{}:{}  includes {}  [syntactic, by name]", value, r.get::<_, i64>(1)?, r.get::<_, String>(0)?))).map_err(|e| e.to_string())?;
            for row in rows { lines.push(row.map_err(|e| e.to_string())?); }
        }
        "includers" => {
            let name = Path::new(value).file_name().and_then(|s| s.to_str()).unwrap_or("");
            let mut stmt = db.prepare("SELECT path, line, target FROM includes ORDER BY path, line").map_err(|e| e.to_string())?;
            let rows = stmt.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?, r.get::<_, String>(2)?))).map_err(|e| e.to_string())?;
            for row in rows {
                let (path, line, target) = row.map_err(|e| e.to_string())?;
                if !name.is_empty() && (target == name || target.strip_suffix(name).is_some_and(|prefix| prefix.ends_with('/'))) {
                    if lines.len() < 20 { lines.push(format!("included by {path}:{line}  [syntactic, by name]")); }
                    else { omitted += 1; }
                }
            }
        }
        _ => return Err("unknown code query".into()),
    }
    if lines.is_empty() { println!("code: none recorded [syntactic]"); }
    else {
        for line in lines.iter().take(20) { println!("{}", crate::text::cut(line, 200)); }
        if lines.len() + omitted > 20 { println!("shown 20 of {}", lines.len() + omitted); }
    }
    Ok(())
}

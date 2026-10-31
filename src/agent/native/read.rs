// A disposable per-session marker prevents repeated whole-file read refusals.
use super::scope::Scope;
use serde_json::Value;
use std::fs::{self, OpenOptions};
pub(super) fn nudge(scope: &Scope, input: &Value, args: &Value) -> Option<String> {
    let args = args.as_object()?;
    if args
        .keys()
        .any(|k| !["file_path", "offset", "limit"].contains(&k.as_str()))
        || args.contains_key("offset")
        || args.contains_key("limit")
    {
        return None;
    }
    let path = scope.path(Some(args.get("file_path")?.as_str()?))?;
    let relative = scope.relative(&path)?;
    crate::code::languages::for_path(relative)?;
    let text = fs::read_to_string(&path).ok()?;
    if text.contains('\0') || text.lines().count() <= 200 {
        return None;
    }
    let session = input
        .get("session_id")?
        .as_str()
        .filter(|s| !s.is_empty())?;
    let key = crate::record::sha256_hex(&serde_json::to_vec(&(session, &path)).ok()?);
    let git_dir = crate::git::run(&scope.root, &["rev-parse", "--absolute-git-dir"]).ok()?;
    let dir =
        std::path::Path::new(git_dir.trim_end_matches('\n')).join("whosaidso/cache/agent-reads");
    fs::create_dir_all(&dir).ok()?;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(dir.join(key))
        .ok()?;
    Some(format!(
        "Run whosaidso code outline {} then whosaidso code show '<name>'; use Read with offset or limit for a slice.",
        super::quote(relative)
    ))
}

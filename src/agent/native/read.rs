// Suggest symbol reads only for long, whole source files.
use super::scope::Scope;
use serde_json::Value;
use std::fs;
pub(super) fn nudge(scope: &Scope, args: &Value) -> Option<String> {
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
    Some(format!(
        "Run whosaidso code outline {} then whosaidso code show '<name>'; use Read with offset or limit for a slice.",
        super::quote(relative)
    ))
}

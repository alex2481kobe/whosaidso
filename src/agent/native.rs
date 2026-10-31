// Native tool refusals suggest commands without changing the original tool's meaning.
mod read;
mod scope;
use super::{install::quote, redirect::deny};
use scope::Scope;
use serde_json::Value;

pub(super) fn response(input: &Value) -> Option<Value> {
    if input.get("hook_event_name")?.as_str()? != "PreToolUse" {
        return None;
    }
    let name = input.get("tool_name")?.as_str()?;
    if ![
        "Read",
        "read_file",
        "Grep",
        "grep_files",
        "Glob",
        "glob_files",
    ]
    .contains(&name)
    {
        return None;
    }
    let scope = Scope::from_input(input)?;
    let args = input.get("tool_input")?;
    let reason = match name {
        "Read" | "read_file" => read::nudge(&scope, input, args),
        "Grep" | "grep_files" => grep(&scope, args),
        _ => glob(&scope, args),
    }?;
    Some(deny(&reason))
}
fn string<'a>(args: &'a Value, key: &str) -> Option<&'a str> {
    args.get(key)?
        .as_str()
        .filter(|s| !s.chars().any(char::is_control))
}
fn boolean(args: &Value, key: &str, default: bool) -> Option<bool> {
    args.get(key).map_or(Some(default), Value::as_bool)
}
fn grep(scope: &Scope, args: &Value) -> Option<String> {
    if args.as_object()?.keys().any(|k| {
        ![
            "pattern",
            "path",
            "glob",
            "-i",
            "-n",
            "multiline",
            "output_mode",
            "head_limit",
            "offset",
            "-C",
        ]
        .contains(&k.as_str())
    }) {
        return None;
    }
    let pattern = string(args, "pattern").filter(|s| !s.is_empty() && s.is_ascii())?;
    // PCRE extensions and escapes have no portable Git ERE equivalent.
    if pattern.contains(['\\', '{', '}'])
        || pattern.contains("(?")
        || pattern
            .as_bytes()
            .windows(2)
            .any(|w| matches!(w, [b'*' | b'+' | b'?', b'*' | b'+' | b'?']))
        || regex::Regex::new(pattern).is_err()
    {
        return None;
    }
    if boolean(args, "multiline", false)? || !boolean(args, "-n", true)? {
        return None;
    }
    let mut command = vec![
        "whosaidso grep --regex".to_owned(),
        if boolean(args, "-i", false)? {
            "-i"
        } else {
            "-s"
        }
        .into(),
    ];
    match args
        .get("output_mode")
        .map_or(Some("files_with_matches"), Value::as_str)?
    {
        "content" => {}
        "files_with_matches" => command.push("-l".into()),
        _ => return None,
    }
    if args.get("glob").is_some() {
        let glob = string(args, "glob")?;
        if !portable_glob(glob) {
            return None;
        }
        command.push(format!("-g {}", quote(glob)));
    }
    let path = scope.path(if args.get("path").is_some() {
        Some(string(args, "path")?)
    } else {
        None
    })?;
    let relative = scope.relative(&path)?;
    if !relative.is_empty() {
        if args
            .get("glob")
            .is_some_and(|g| !path.is_dir() || g.as_str().is_some_and(|s| s.contains('/')))
        {
            return None;
        }
        if path.is_dir() {
            command.push(format!("--in {}", quote(&format!("{relative}/"))));
        } else if path.is_file() && !relative.contains(['*', '?', '[', ']', '!', '\\']) {
            command.push(format!("-g {}", quote(&format!("./{relative}"))));
        } else {
            return None;
        }
    }
    for (key, flag) in [
        ("head_limit", "--limit"),
        ("offset", "--offset"),
        ("-C", "-C"),
    ] {
        if let Some(value) = args.get(key) {
            let value = value.as_u64()?;
            if key == "head_limit" && value == 0 {
                return None;
            }
            command.push(format!("{flag} {value}"));
        }
    }
    command.push(format!("-- {}", quote(pattern)));
    Some(format!("Run {}.", command.join(" ")))
}
fn glob(scope: &Scope, args: &Value) -> Option<String> {
    if args
        .as_object()?
        .keys()
        .any(|k| !["pattern", "path"].contains(&k.as_str()))
    {
        return None;
    }
    let pattern = string(args, "pattern").filter(|s| portable_glob(s) && !s.starts_with('!'))?;
    let path = scope.path(if args.get("path").is_some() {
        Some(string(args, "path")?)
    } else {
        None
    })?;
    if !path.is_dir() {
        return None;
    }
    let prefix = scope.relative(&path)?;
    let pattern = if prefix.is_empty() {
        pattern.to_owned()
    } else {
        format!("{prefix}/{pattern}")
    };
    Some(format!(
        "Run git -C {} ls-files --cached --others --exclude-standard -- {}.",
        quote(scope.root.to_str()?),
        quote(&format!(":(glob){pattern}"))
    ))
}

fn portable_glob(pattern: &str) -> bool {
    !pattern.is_empty()
        && !pattern.starts_with('/')
        && !pattern.contains(['\\', '{', '}'])
        && !pattern.split('/').any(|part| part == "..")
}

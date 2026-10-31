// Merge only the owned hook entry; never replace other agent configuration.
use serde_json::{Value, json};
use std::{fs, path::PathBuf};
const MARKER: &str = " # whosaidso-agent";

pub(super) fn quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', "'\"'\"'"))
}
pub(super) fn command(args: &[String]) -> Result<String, String> {
    let (client, uninstall) = match args {
        [client] => (client.as_str(), false),
        [client, flag] if flag == "--uninstall" => (client.as_str(), true),
        _ => return Err("usage: install-agent claude|codex [--uninstall]".into()),
    };
    let relative = match client {
        "claude" => ".claude/settings.json",
        "codex" => ".codex/hooks.json",
        _ => return Err("install-agent: expected claude or codex".into()),
    };
    let home = PathBuf::from(std::env::var_os("HOME").ok_or("HOME is not set")?);
    let path = home.join(relative);
    let skills = path
        .parent()
        .ok_or("missing client directory")?
        .join("skills");
    super::skills::check(&skills)?;
    let previous = match fs::read(&path) {
        Ok(bytes) => Some(bytes),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => None,
        Err(e) => return Err(e.to_string()),
    };
    if uninstall && previous.is_none() {
        super::skills::apply(&skills, true)?;
        return Ok("Agent skills removed; no agent hook installed".into());
    }
    let mut config: Value = match &previous {
        Some(bytes) => {
            serde_json::from_slice(bytes).map_err(|e| format!("{}: {e}", path.display()))?
        }
        None => json!({}),
    };
    let object = config.as_object_mut().ok_or("settings must be an object")?;
    let hooks = object
        .entry("hooks")
        .or_insert_with(|| json!({}))
        .as_object_mut()
        .ok_or("hooks must be an object")?;
    let groups = hooks
        .entry("PreToolUse")
        .or_insert_with(|| json!([]))
        .as_array_mut()
        .ok_or("PreToolUse must be an array")?;
    groups.retain_mut(|group| {
        if let Some(entries) = group.get_mut("hooks").and_then(Value::as_array_mut) {
            let before = entries.len();
            entries.retain(|h| {
                !h.get("command")
                    .and_then(Value::as_str)
                    .is_some_and(|s| s.ends_with(MARKER))
            });
            return before == entries.len() || !entries.is_empty();
        }
        true
    });
    if !uninstall {
        let bin = std::env::current_exe().map_err(|e| e.to_string())?;
        let bin = bin.to_str().ok_or("executable path is not UTF-8")?;
        groups.push(json!({"matcher":"^(Bash|Read|Grep|Glob|read_file|grep_files|glob_files)$", "hooks":[{"type":"command",
            "command":format!("{} agent-hook {client} || :{MARKER}",quote(bin)),"timeout":5}]}));
    }
    if groups.is_empty() {
        hooks.remove("PreToolUse");
    }
    if hooks.is_empty() {
        object.remove("hooks");
    }
    let mut bytes = serde_json::to_vec_pretty(&config).map_err(|e| e.to_string())?;
    bytes.push(b'\n');
    super::skills::apply(&skills, uninstall)?;
    if previous.as_deref() != Some(&bytes) {
        fs::create_dir_all(path.parent().ok_or("missing configuration directory")?)
            .map_err(|e| e.to_string())?;
        fs::write(&path, bytes).map_err(|e| e.to_string())?;
    }
    Ok(if uninstall {
        "Agent hook and skills removed".into()
    } else {
        format!(
            "Agent hook and skills installed for {client}; see WhoSaidSo's AGENTS.md#using-whosaidso; restart the client{}.",
            if client == "codex" {
                " and trust the hook in /hooks before it will run"
            } else {
                ""
            }
        )
    })
}

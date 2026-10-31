// Translate only forms with an exact argument and output contract.
use super::{install::quote, shell};
use serde_json::{Value, json};

pub(super) const RETRIEVAL_ACTION: &str = "Use whosaidso code/grep, or WHOSAIDSO_ESCAPE='<index gap reason>' for this unsupported search/read form.";

pub(super) fn deny(reason: &str) -> Value {
    json!({"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":reason}})
}
pub(super) fn response(input: &Value) -> Option<Value> {
    if input.get("hook_event_name")?.as_str()? != "PreToolUse"
        || input.get("tool_name")?.as_str()? != "Bash"
    {
        return None;
    }
    let tool = input.get("tool_input")?;
    let command = tool.get("command")?.as_str()?;
    let commands = shell::commands(command)?;
    if let Some(response) = super::gate::response(&commands, command) {
        return Some(response);
    }
    if commands.len() != 1 || !commands[0].iter().all(|w| w.literal) {
        return None;
    }
    let concerned = shell::search_or_read(&commands[0]);
    if !concerned {
        return None;
    }
    let supported = commands.len() == 1
        && commands[0].iter().all(|w| w.literal)
        && supported(
            &commands[0]
                .iter()
                .map(|w| w.text.as_str())
                .collect::<Vec<_>>(),
        );
    if !supported {
        return Some(deny(RETRIEVAL_ACTION));
    }
    if !available(&commands[0][0].text) {
        return Some(
            json!({"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"Original utility unavailable on PATH; preserving the original command."}}),
        );
    }
    let bin = std::env::current_exe().ok()?;
    let mut updated = tool.clone();
    updated["command"] = format!(
        "{} agent-run {}",
        quote(bin.to_str()?),
        commands[0]
            .iter()
            .map(|w| quote(&w.text))
            .collect::<Vec<_>>()
            .join(" ")
    )
    .into();
    Some(
        json!({"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":updated}}),
    )
}
pub(super) fn supported(args: &[&str]) -> bool {
    match args {
        ["cat", "--", path] => *path != "-",
        ["cat", path] => !path.starts_with('-'),
        ["grep" | "rg", "-n", "-F", "--", pattern, path] => {
            !pattern.is_empty() && !pattern.contains('\n') && *path != "-"
        }
        _ => false,
    }
}

pub(super) fn available(program: &str) -> bool {
    use std::os::unix::fs::PermissionsExt;
    std::env::var_os("PATH").is_some_and(|paths| {
        std::env::split_paths(&paths).any(|dir| {
            std::fs::metadata(dir.join(program))
                .is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
        })
    })
}

// Shared policy for both clients; exceptions are recorded outside project records.
use super::{
    redirect::deny,
    runners,
    shell::{self, Word},
};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    fs::{self, OpenOptions},
    io::Write,
    path::Path,
};
const MARKERS: [&str; 2] = ["FULL_SUITE", "WHOSAIDSO_ESCAPE"];
type Reasons = BTreeMap<String, String>;

pub(super) fn response(commands: &[Vec<Word>], original: &str) -> Option<Value> {
    let inherited = MARKERS
        .iter()
        .filter_map(|key| std::env::var(key).ok().map(|v| ((*key).to_owned(), v)))
        .collect();
    let mut used = Vec::new();
    if let Some(reason) = inspect(commands, &inherited, &mut used, 0) {
        return Some(deny(&reason));
    }
    if used.is_empty() {
        return None;
    }
    let logged = log(&used, original);
    Some(
        json!({"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":if logged {
        "Reason marker recorded; index coverage is not complete."
    } else { "Reason marker allowed but audit log unavailable; index coverage is not complete." }}}),
    )
}
fn inspect(
    commands: &[Vec<Word>],
    inherited: &Reasons,
    used: &mut Vec<(String, String)>,
    depth: usize,
) -> Option<String> {
    if depth > 8 {
        return None;
    }
    let mut persistent = inherited.clone();
    for (command_index, words) in commands.iter().enumerate() {
        if words.is_empty() {
            continue;
        }
        let mut skip = false;
        let words: Vec<_> = words
            .iter()
            .filter(|w| {
                if skip {
                    skip = false;
                    return false;
                }
                if w.text == "<redirection>" {
                    skip = true;
                    return false;
                }
                true
            })
            .collect();
        if words.is_empty() {
            continue;
        }
        let texts: Vec<_> = words.iter().map(|w| w.text.as_str()).collect();
        if texts[0] == "export" && command_index == 0 {
            for word in &words[1..] {
                assignment(word, &mut persistent);
            }
            continue;
        }
        if texts[0] == "unset" {
            for word in &texts[1..] {
                persistent.remove(*word);
            }
            continue;
        }
        let mut reasons = persistent.clone();
        let mut start = 0;
        while start < words.len() {
            if assignment(&words[start], &mut reasons) {
                start += 1;
                continue;
            }
            let base = basename(texts[start]);
            match base {
                "env" | "command" | "exec" | "nohup" | "time" | "then" | "do" | "if" | "!" => {
                    start += 1;
                }
                "timeout" => {
                    start = (start + 2).min(words.len());
                }
                "--" | "-p" if start > 0 => {
                    start += 1;
                }
                "-i" | "--ignore-environment" if start > 0 => {
                    reasons.clear();
                    start += 1;
                }
                flag if start > 0 && flag.starts_with("--unset=") => {
                    reasons.remove(&flag[8..]);
                    start += 1;
                }
                "-u" | "--unset" if start > 0 && start + 1 < words.len() => {
                    reasons.remove(texts[start + 1]);
                    start += 2;
                }
                _ => break,
            }
        }
        if start == words.len() {
            // An assignment-only shell command persists in this shell invocation.
            if command_index == 0 {
                persistent = reasons;
            }
            continue;
        }
        let mut args = texts[start..].to_vec();
        args[0] = basename(args[0]);
        if ["sh", "bash", "zsh"].contains(&args[0])
            && args.get(1).is_some_and(|s| ["-c", "-lc"].contains(s))
        {
            if let Some(inner) = args.get(2).and_then(|s| shell::commands(s))
                && let Some(reason) = inspect(&inner, &reasons, used, depth + 1)
            {
                return Some(reason);
            }
        }
        if runners::unscoped(&args) {
            if !use_reason("FULL_SUITE", &reasons, used) {
                return Some(
                    "Run a scoped test, or set FULL_SUITE='<why this full suite is needed>'."
                        .into(),
                );
            }
        }
        let selected = &words[start..];
        let mut normalized = selected
            .iter()
            .map(|w| Word {
                text: w.text.clone(),
                literal: w.literal,
            })
            .collect::<Vec<_>>();
        normalized[0].text = args[0].to_owned();
        if shell::search_or_read(&normalized) {
            if use_reason("WHOSAIDSO_ESCAPE", &reasons, used) {
                continue;
            }
            if depth != 0
                || commands.len() != 1
                || start != 0
                || !words.iter().all(|w| w.literal)
                || !super::redirect::supported(&args)
            {
                return Some(super::redirect::RETRIEVAL_ACTION.into());
            }
        }
    }
    None
}
fn basename(text: &str) -> &str {
    text.rsplit('/').next().unwrap_or(text)
}
fn assignment(word: &Word, reasons: &mut Reasons) -> bool {
    let Some((key, value)) = word.text.split_once('=') else {
        return false;
    };
    if key.is_empty() || !key.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'_') {
        return false;
    }
    if MARKERS.contains(&key) {
        reasons.insert(
            key.to_owned(),
            if word.literal {
                value.to_owned()
            } else {
                String::new()
            },
        );
    }
    true
}
fn use_reason(key: &str, reasons: &Reasons, used: &mut Vec<(String, String)>) -> bool {
    let Some(reason) = reasons.get(key).filter(|s| !s.trim().is_empty()) else {
        return false;
    };
    used.push((key.to_owned(), reason.clone()));
    true
}
fn log(used: &[(String, String)], command: &str) -> bool {
    let Some(home) = std::env::var_os("HOME") else {
        return false;
    };
    let dir = Path::new(&home).join(".whosaidso");
    let result = (|| -> std::io::Result<()> {
        fs::create_dir_all(&dir)?;
        let mut file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(dir.join("agent-escapes.jsonl"))?;
        for (marker, reason) in used {
            writeln!(
                file,
                "{}",
                json!({"marker":marker,"reason":reason,"command":command,"complete":false})
            )?;
        }
        Ok(())
    })();
    result.is_ok()
}

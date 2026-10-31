// Session markers are shared by tool aliases and repositories; hook infrastructure fails open.
use serde_json::{Value, json};
use std::{
    fs::{self, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
};

pub(super) fn first_hint(input: &Value, kind: &str, hint: &str) -> bool {
    let Some(home) = std::env::var_os("HOME") else {
        return false;
    };
    let dir = PathBuf::from(home).join(".whosaidso");
    let session = input
        .get("session_id")
        .and_then(Value::as_str)
        .filter(|s| !s.trim().is_empty());
    let Some(session) = session else {
        log(&dir, None, kind, "pass", "session_id missing", hint);
        return false;
    };
    let key = crate::record::sha256_hex(&serde_json::to_vec(&(session, kind)).unwrap());
    let cache = dir.join("cache/agent-hints");
    let path = cache.join(key);
    let created = fs::create_dir_all(&cache)
        .and_then(|_| OpenOptions::new().write(true).create_new(true).open(&path));
    let (first, reason) = match created {
        Ok(_) => (true, "first hint".to_owned()),
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {
            (false, "hint already delivered".to_owned())
        }
        Err(e) => (false, format!("{}: {e}", path.display())),
    };
    log(
        &dir,
        Some(session),
        kind,
        if first { "deny" } else { "pass" },
        &reason,
        hint,
    );
    first
}
fn log(dir: &Path, session: Option<&str>, kind: &str, decision: &str, reason: &str, hint: &str) {
    let result = (|| -> std::io::Result<()> {
        fs::create_dir_all(dir)?;
        let mut file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(dir.join("agent-hints.jsonl"))?;
        file.lock()?;
        writeln!(
            file,
            "{}",
            json!({"session_id":session,"kind":kind,"decision":decision,"reason":reason,"hint":hint})
        )
    })();
    if let Err(e) = result {
        eprintln!("{}: {e}", dir.join("agent-hints.jsonl").display());
    }
}

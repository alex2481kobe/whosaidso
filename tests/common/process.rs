use super::BIN;
use std::{
    ffi::OsString,
    path::Path,
    process::{Command, Output},
    sync::OnceLock,
};
pub fn tool_path() -> &'static OsString {
    static PATH: OnceLock<OsString> = OnceLock::new();
    PATH.get_or_init(|| {
        let path = std::env::var_os("PATH").unwrap_or_default();
        // Apple's launcher repeats developer-tool discovery for every Git subprocess.
        if cfg!(target_os = "macos")
            && let Ok(out) = Command::new("xcrun").args(["--find", "git"]).output()
            && out.status.success()
            && let Ok(git) = std::str::from_utf8(&out.stdout)
            && let Some(dir) = Path::new(git.trim()).parent()
        {
            return std::env::join_paths(
                std::iter::once(dir.to_path_buf()).chain(std::env::split_paths(&path)),
            )
            .expect("tool search path");
        }
        path
    })
}
pub fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("PATH", tool_path())
        .env("HOME", root.parent().unwrap())
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_AUTHOR_NAME", "Test")
        .env("GIT_AUTHOR_EMAIL", "test@example.com")
        .env("GIT_CONFIG_COUNT", "2")
        .env("GIT_CONFIG_KEY_0", "user.name")
        .env("GIT_CONFIG_VALUE_0", "Test")
        .env("GIT_CONFIG_KEY_1", "user.email")
        .env("GIT_CONFIG_VALUE_1", "test@example.com")
        .env("GIT_COMMITTER_NAME", "Test")
        .env("GIT_COMMITTER_EMAIL", "test@example.com");
    for name in [
        "WHOSAIDSO_BLOBS",
        "WHOSAIDSO_ARCHIVE",
        "WHOSAIDSO_HOME",
        "WHOSAIDSO_PRUNE_HOOK",
        "WHOSAIDSO_PRUNE_TOUCH_HOOK",
        "WHOSAIDSO_PRUNE_TRASH_HOOK",
        "WHOSAIDSO_PRUNE_DELETE_HOOK",
    ] {
        c.env_remove(name);
    }
    c.env("WHOSAIDSO_HOME", root.parent().unwrap().join("registry"));
    if program == "git" {
        c.args(["-c", "init.defaultBranch=main"]);
    }
    let bin = root.join(".git/bound-bin");
    if bin.is_dir() {
        c.env("PATH", std::env::join_paths(std::iter::once(bin)
            .chain(std::env::split_paths(tool_path()))).unwrap());
    }
    c.args(args);
    c
}

pub fn success(out: Output) -> String {
    assert!(out.status.success(), "{out:?}");
    String::from_utf8(out.stdout).unwrap()
}

pub fn failure(out: Output, expected: &str) -> String {
    assert_eq!(out.status.code(), Some(1), "{out:?}");
    let text = format!(
        "{}{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    assert!(text.contains(expected), "expected {expected:?}: {text}");
    text
}

pub fn git(root: &Path, args: &[&str]) -> String {
    success(command(root, "git", args).output().unwrap())
}
pub fn cli(root: &Path, args: &[&str]) -> String {
    success(command(root, BIN, args).output().unwrap())
}
pub fn bad(root: &Path, args: &[&str], expected: &str) -> String {
    failure(command(root, BIN, args).output().unwrap(), expected)
}
pub fn input(root: &Path, args: &[&str], bytes: &[u8]) -> String {
    use std::{io::Write, process::Stdio};
    let mut child = command(root, "git", args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child.stdin.take().unwrap().write_all(bytes).unwrap();
    success(child.wait_with_output().unwrap()).trim().to_owned()
}

// Row equality stays independent of the separately tested coverage diagnostics.
pub fn code_rows(root: &Path, args: &[&str]) -> String {
    code_rows_text(cli(root, args))
}
pub fn code_rows_text(text: String) -> String {
    text.lines()
        .filter(|line| !line.starts_with("gap: ") && !line.starts_with("gaps: "))
        .map(|line| format!("{line}\n"))
        .collect()
}

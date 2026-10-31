mod common;
use common::process::*;
use common::*;
use serde_json::{Value, json};
use std::{io::Write, process::Stdio};

fn hook(f: &Fixture, client: &str, command_text: &str) -> Value {
    let payload = json!({"hook_event_name":"PreToolUse", "cwd":f.root,
        "tool_name":"Bash", "tool_input":{"command":command_text,"description":"keep me","timeout":1234}});
    hook_value(f, client, payload)
}

fn hook_value(f: &Fixture, client: &str, payload: Value) -> Value {
    let mut child = command(&f.root, BIN, &["agent-hook", client])
        .env_remove("FULL_SUITE")
        .env_remove("WHOSAIDSO_ESCAPE")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(payload.to_string().as_bytes())
        .unwrap();
    let out = success(child.wait_with_output().unwrap());
    serde_json::from_str(&out).unwrap_or_else(|_| panic!("invalid hook response: {out}"))
}

#[test]
fn installation_is_idempotent_and_uninstall_preserves_other_hooks() {
    let f = Fixture::empty();
    let home = f.root.parent().unwrap();
    for (client, file) in [
        ("claude", ".claude/settings.json"),
        ("codex", ".codex/hooks.json"),
    ] {
        let path = home.join(file);
        write(
            &path,
            r#"{"custom":true,"hooks":{"PreToolUse":[{"matcher":"Other","hooks":[{"type":"command","command":"echo unrelated"}]}]}}"#,
        );
        cli(&f.root, &["install-agent", client]);
        let first = std::fs::read(&path).unwrap();
        cli(&f.root, &["install-agent", client]);
        assert_eq!(first, std::fs::read(&path).unwrap());
        let installed = value(&path);
        assert_eq!(
            installed["hooks"]["PreToolUse"].as_array().unwrap().len(),
            2
        );
        assert!(installed.to_string().contains("agent-hook"));
        cli(&f.root, &["install-agent", client, "--uninstall"]);
        assert_eq!(
            value(&path),
            json!({"custom":true,"hooks":{"PreToolUse":[{"matcher":"Other","hooks":[{"type":"command","command":"echo unrelated"}]}]}})
        );
        cli(&f.root, &["install-agent", client, "--uninstall"]);
    }
}

#[test]
fn each_redirect_preserves_arguments_bytes_and_exit_status() {
    let f = Fixture::sources(&[("space file.txt", "alpha\n雪 alpha\nno final newline")]);
    for client in ["claude", "codex"] {
        for original in [
            "cat -- 'space file.txt'",
            "grep -n -F -- alpha 'space file.txt'",
            "rg -n -F -- alpha 'space file.txt'",
        ] {
            let response = hook(&f, client, original);
            let updated = &response["hookSpecificOutput"]["updatedInput"];
            let Some(rewritten) = updated["command"].as_str() else {
                assert!(response.to_string().contains("unavailable"), "{response}");
                eprintln!("native comparison unavailable: {original}");
                continue;
            };
            assert_eq!(updated["description"], "keep me");
            assert_eq!(updated["timeout"], 1234);
            assert!(rewritten.contains("agent-run"), "{response}");
            let expected = command(&f.root, "sh", &["-c", original]).output().unwrap();
            let actual = command(&f.root, "sh", &["-c", rewritten]).output().unwrap();
            assert_eq!(
                (actual.status.code(), actual.stdout, actual.stderr),
                (expected.status.code(), expected.stdout, expected.stderr)
            );
        }
    }
}

#[test]
fn unsupported_forms_are_explicit_and_unrelated_or_bad_payloads_fail_open() {
    let f = Fixture::empty();
    for client in ["claude", "codex"] {
        for input in [
            "rg -i alpha .",
            "cat a b",
            "cat x | wc -l",
            "grep -n -F -- alpha '$HOME/file' ; echo ok",
        ] {
            let r = hook(&f, client, input);
            assert_eq!(
                r["hookSpecificOutput"]["permissionDecision"], "deny",
                "{input}: {r}"
            );
            assert!(r.to_string().contains("WHOSAIDSO_ESCAPE"));
        }
        assert_eq!(hook(&f, client, "echo 'cargo test'"), json!({}));
        let mut child = command(&f.root, BIN, &["agent-hook", client])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        child.stdin.take().unwrap().write_all(b"bad json").unwrap();
        assert_eq!(success(child.wait_with_output().unwrap()).trim(), "{}");
        let mut child = command(&f.root, BIN, &["agent-hook", client])
            .env("PATH", f.root.join("absent-bin"))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        child.stdin.take().unwrap().write_all(br#"{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rg -n -F -- needle file"}}"#).unwrap();
        let response: Value =
            serde_json::from_str(&success(child.wait_with_output().unwrap())).unwrap();
        assert!(response["hookSpecificOutput"].get("updatedInput").is_none());
        assert!(response.to_string().contains("unavailable"));
    }
}

#[test]
fn redirected_reads_are_complete_and_search_fallbacks_preserve_failures() {
    let f = Fixture::empty();
    let bytes = [vec![b'x'; 100_000], vec![0, 255, b'\n']].concat();
    write(&f.root.join("raw"), &bytes);
    let read = command(&f.root, BIN, &["agent-run", "cat", "--", "raw"])
        .output()
        .unwrap();
    assert!(read.status.success());
    assert_eq!(read.stdout, bytes);
    for program in ["grep", "rg"] {
        if command(&f.root, program, &["--help"]).output().is_err() {
            eprintln!("native comparison unavailable: {program}");
            continue;
        }
        for (path, body) in [
            ("empty", b"".as_slice()),
            ("no-match", b"other\n"),
            ("binary", b"alpha\0\xff\n"),
            ("crlf", b"alpha\r\n"),
            ("last", b"alpha"),
            ("bom", b"\xef\xbb\xbfalpha\n"),
        ] {
            write(&f.root.join(path), body);
            let args = ["-n", "-F", "--", "alpha", path];
            let expected = command(&f.root, program, &args).output().unwrap();
            let actual = command(&f.root, BIN, &[&["agent-run", program][..], &args].concat())
                .output()
                .unwrap();
            assert_eq!(
                (actual.status.code(), actual.stdout, actual.stderr),
                (expected.status.code(), expected.stdout, expected.stderr)
            );
        }
        let args = ["-n", "-F", "--", "alpha", "missing"];
        let expected = command(&f.root, program, &args).output().unwrap();
        let actual = command(&f.root, BIN, &[&["agent-run", program][..], &args].concat())
            .output()
            .unwrap();
        assert_eq!(
            (actual.status.code(), actual.stdout, actual.stderr),
            (expected.status.code(), expected.stdout, expected.stderr)
        );
    }
}

#[test]
fn broken_installed_executable_fails_open() {
    let f = Fixture::empty();
    let copy = f.root.join("hook binary");
    std::fs::copy(BIN, &copy).unwrap();
    success(
        command(&f.root, copy.to_str().unwrap(), &["install-agent", "codex"])
            .output()
            .unwrap(),
    );
    let settings = value(&f.root.parent().unwrap().join(".codex/hooks.json"));
    let shell = settings["hooks"]["PreToolUse"][0]["hooks"][0]["command"]
        .as_str()
        .unwrap();
    std::fs::rename(&copy, f.root.join("moved binary")).unwrap();
    assert!(
        command(&f.root, "sh", &["-c", shell])
            .output()
            .unwrap()
            .status
            .success()
    );
}

#[path = "v032_agents/gate.rs"]
mod gate;

#[path = "v032_agents/native.rs"]
mod native;

#[path = "v032_agents/help.rs"]
mod help;

#[path = "v032_agents/indexer.rs"]
mod indexer;

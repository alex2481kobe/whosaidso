// Third-review regressions through the real binary in one isolated Git project.
use rusqlite::Connection;
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::{Command, Output, Stdio},
    thread, time::{Duration, Instant, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn run(root: &Path, program: &str, args: &[&str]) -> Output { command(root, program, args).output().unwrap() }
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(out.status.success(), "{args:?}: {} {}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr));
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String { ok(root, "git", args) }
fn cli(root: &Path, args: &[&str]) -> String { ok(root, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, bytes: &[u8]) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn addressed(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap();
    let sha: String = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect();
    let path = dir.join(format!("{sha}.json")); write(&path, &bytes); path
}
fn wait(path: &Path) {
    let start = Instant::now();
    while !path.exists() {
        assert!(start.elapsed() < Duration::from_secs(8), "missing {}", path.display());
        thread::sleep(Duration::from_millis(10));
    }
}
#[test]
fn reviewed_proofs_records_history_and_cache() {
    let root = std::env::temp_dir().join(format!("hard3-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    let records = root.join(".whosaidso");
    write(&records.join("phases/p.json"), br#"{"title":"Phase","order":1}"#);
    write(&records.join("goals/g.json"), br#"{"title":"Goal","done_when":"done","check":"test","broader":"p"}"#);
    let instrument = records.join("instruments/test.json");
    write(&instrument, br#"{"run":["sh","-c","printf ready > .git/ready; sleep 2; ./test; rc=$?; sleep 2; exit \"$rc\""],"answers":"works"}"#);
    write(&root.join("test"), b"#!/bin/sh\nprintf A > .git/ran\nexit 1\n");
    fs::set_permissions(root.join("test"), fs::Permissions::from_mode(0o755)).unwrap();
    write(&root.join("dir/child.rs"), b"fn directoryneedle() {}\n");
    cli(&root, &["find", "directoryneedle"]);
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "baseline"]);
    let a = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    git(&root, &["checkout", "-b", "alternate"]);
    write(&root.join("test"), b"#!/bin/sh\nprintf B > .git/ran\nexit 0\n");
    git(&root, &["add", "test"]); git(&root, &["commit", "-m", "spectraltamarind"]);
    let b = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    git(&root, &["checkout", "main"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", &a]);

    // 1: A -> B -> A is clean at both ends, but B's result cannot prove A.
    let child = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prove", "g"]).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
    wait(&root.join(".git/ready")); git(&root, &["checkout", "alternate"]);
    wait(&root.join(".git/ran")); git(&root, &["checkout", "main"]);
    assert_eq!(fs::read(root.join(".git/ran")).unwrap(), b"B");
    let out = child.wait_with_output().unwrap();
    assert_eq!(out.status.code(), Some(1), "1: {out:?}");
    assert!(String::from_utf8_lossy(&out.stderr).contains("HEAD reflog changed"), "1: {out:?}");
    assert!(!records.join("proofs").exists(), "1: no proof after HEAD movements");
    write(&instrument, br#"{"run":["true"],"answers":"works"}"#);
    git(&root, &["add", ".whosaidso/instruments"]); git(&root, &["commit", "-m", "instrument"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    assert!(cli(&root, &["prove", "g"]).contains(" pass "));
    let proofs = records.join("proofs");
    let valid = fs::read_dir(&proofs).unwrap().next().unwrap().unwrap().path();
    let mut proof: serde_json::Value = serde_json::from_slice(&fs::read(&valid).unwrap()).unwrap();
    assert!(cli(&root, &["show", "g"]).contains("state: done"));

    // 2: Git accepts uppercase object IDs, but record commits are canonical lowercase.
    fs::remove_file(&valid).unwrap(); proof["commit"] = a.to_uppercase().into();
    let upper = addressed(&proofs, &proof);
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert!(out.status.code() == Some(1) && String::from_utf8_lossy(&out.stdout).contains("proof: invalid commit"), "2: {out:?}");
    assert!(!cli(&root, &["show", "g"]).contains("proof:"));
    fs::remove_file(upper).unwrap(); proof["commit"] = git(&root, &["rev-parse", "HEAD"]).trim().into();

    // 4: Warm valid rows disappear when the source becomes invalid, even at the same path.
    proof["output_tail"] = "validatorneedle".into();
    let valid = addressed(&proofs, &proof);
    assert!(cli(&root, &["find", "kind:proof", "validatorneedle"]).contains("proof g"));
    let wrong = proofs.join("wrong.json"); fs::rename(&valid, &wrong).unwrap();
    let out = run(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert!(out.status.code() == Some(1) && String::from_utf8_lossy(&out.stdout).contains("proof filename"));
    assert!(cli(&root, &["find", "kind:proof", "validatorneedle"]).contains("no indexed match"), "4: misnamed proof");
    fs::remove_file(wrong).unwrap(); proof["result"] = "pass".into();
    let invalid = addressed(&proofs, &proof);
    assert!(cli(&root, &["find", "kind:proof", "validatorneedle"]).contains("no indexed match"), "4: inconsistent proof");
    fs::remove_file(invalid).unwrap(); proof.as_object_mut().unwrap().remove("result"); addressed(&proofs, &proof);
    assert!(cli(&root, &["check"]).contains("check: ok"));

    // 3: Missing, rewound and fast-forwarded main tips each define the visible history.
    git(&root, &["update-ref", "refs/heads/main", &b]);
    assert!(cli(&root, &["find", "kind:commit", "spectraltamarind"]).contains("spectraltamarind"));
    git(&root, &["update-ref", "-d", "refs/heads/main"]);
    assert!(cli(&root, &["find", "kind:commit", "spectraltamarind"]).contains("no indexed match"), "3: removed main");
    git(&root, &["update-ref", "refs/heads/main", &b]);
    assert!(cli(&root, &["find", "kind:commit", "spectraltamarind"]).contains("spectraltamarind"));
    git(&root, &["update-ref", "refs/heads/main", &a]);
    assert!(cli(&root, &["find", "kind:commit", "spectraltamarind"]).contains("no indexed match"), "3: rewound main");
    git(&root, &["update-ref", "refs/heads/main", &b]);
    assert!(cli(&root, &["find", "kind:commit", "spectraltamarind"]).contains("spectraltamarind"));
    assert!(cli(&root, &["find", "kind:commit", "baseline"]).contains("baseline"));

    // 5: A file replacing a tracked directory removes every old child from all indexes.
    assert!(cli(&root, &["grep", "directoryneedle"]).contains("dir/child.rs:1"));
    assert!(cli(&root, &["code", "defs", "directoryneedle"]).contains("dir/child.rs:1"));
    assert!(cli(&root, &["find", "kind:file", "child"]).contains("dir/child.rs"));
    fs::remove_dir_all(root.join("dir")).unwrap(); write(&root.join("dir"), b"replacementneedle\n");
    assert!(cli(&root, &["grep", "directoryneedle"]).contains("grep: no match"), "5: directory replaced");
    assert!(cli(&root, &["code", "defs", "directoryneedle"]).contains("none recorded"));
    assert!(cli(&root, &["find", "kind:file", "child"]).contains("no indexed match"));
    assert!(cli(&root, &["grep", "replacementneedle"]).contains("dir:1"));

    // 6: Old meanings are discarded even when their cached fingerprints still match.
    let db_path = root.join(".git/whosaidso/cache/index.db");
    let db = Connection::open(&db_path).unwrap();
    db.execute_batch("PRAGMA user_version=1; UPDATE text_fts SET body='stalecacheneedle' WHERE path='dir'; CREATE TABLE obsolete(value);").unwrap();
    drop(db);
    assert!(cli(&root, &["grep", "stalecacheneedle"]).contains("grep: no match"), "6: stale version-one rows");
    assert!(cli(&root, &["grep", "replacementneedle"]).contains("dir:1"));
    let db = Connection::open(db_path).unwrap();
    assert_eq!(db.query_row("PRAGMA user_version", [], |r| r.get::<_, i64>(0)).unwrap(), 2, "6");
    assert_eq!(db.query_row("SELECT count(*) FROM sqlite_master WHERE name='obsolete'", [], |r| r.get::<_, i64>(0)).unwrap(), 0, "6");
    fs::remove_dir_all(root).unwrap();
}

// Bounded Git work and disposable text-index rebuilding through the real binary.
use serde_json::json;
use sha2::{Digest, Sha256};
use std::{fs, os::unix::fs::PermissionsExt, path::{Path, PathBuf}, process::{Command, Output}, time::{SystemTime, UNIX_EPOCH}};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", root).env("GIT_CONFIG_NOSYSTEM", "1").env_remove("WHOSAIDSO_BLOBS");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn record(root: &Path, kind: &str, value: serde_json::Value) -> PathBuf {
    let bytes = value.to_string().into_bytes(); let path = root.join(format!(".whosaidso/{kind}/{}.json", hash(&bytes))); write(&path, bytes); path
}
#[test]
fn queries_batch_git_reads_and_rebuild_only_disposable_text() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("proof-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init"]);
    write(&root.join(".whosaidso/.gitignore"), "cache/\n");
    write(&root.join(".whosaidso/phases/p.json"), json!({"title":"Phase","order":1}).to_string());
    write(&root.join(".whosaidso/goals/g.json"), json!({"title":"Goal","done_when":"done","check":"cover-a","broader":"p"}).to_string());
    let instrument = root.join(".whosaidso/instruments/cover-a.json"); write(&instrument.join("wrong-kind"), "directory instead of instrument");
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "wrong object type"]);
    let wrong_kind = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); fs::remove_dir_all(instrument).unwrap();
    write(&root.join(".whosaidso/instruments/cover-a.json"), json!({"run":["true"],"answers":"works"}).to_string());
    write(&root.join("needle.txt"), "abcd exact\nabc elsewhere bcd\n😀éxW unicode\nquote \"needle\"\n");
    fs::File::create(root.join("large.dat")).unwrap().set_len(2 * 1024 * 1024).unwrap();
    git(&root, &["add", "."]); git(&root, &["commit", "-m", "fixture"]);
    let first = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    git(&root, &["commit", "--allow-empty", "-m", "second commit"]);
    let second = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    for (n, commit) in [&first, &second].into_iter().cycle().take(12).enumerate() {
        record(&root, "proofs", json!({"goal":"g","instrument":"cover-a","commit":commit,"exit_code":0,"output_sha256":hash(b""),"output_tail":"","ran_at":n}));
        let bytes = b"%PDF-1.7\n"; write(&root.join(format!(".git/whosaidso/blobs/{}.pdf", hash(bytes))), bytes);
        record(&root, "media", json!({"goal":"g","shows":format!("evidence {n}"),"media_sha256":hash(bytes),"ext":"pdf","byte_size":bytes.len(),"commit":commit,"ran_at":n}));
    }
    let bin = root.join(".whosaidso/cache/bin"); let trace = root.join(".whosaidso/cache/trace");
    let real = success(command(&root, "sh", &["-c", "command -v git"]).output().unwrap());
    write(&bin.join("git"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PROOF_TRACE\"\nif test \"$1 $2\" = 'cat-file --batch-command' && test -n \"$PROOF_REQUESTS\"; then tee \"$PROOF_REQUESTS\" | \"$PROOF_GIT\" \"$@\"; exit $?; fi\nexec \"$PROOF_GIT\" \"$@\"\n");
    fs::set_permissions(bin.join("git"), fs::Permissions::from_mode(0o755)).unwrap();
    let shown = success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["show", "g"]).env("PATH", format!("{}:{}", bin.display(), std::env::var("PATH").unwrap())).env("PROOF_TRACE", &trace).env("PROOF_GIT", real.trim()).output().unwrap());
    assert!(shown.contains("state: done") && shown.contains("proof: pass") && shown.contains("media: evidence 11"));
    let calls = fs::read_to_string(&trace).unwrap();
    assert_eq!(calls.lines().filter(|l| *l == "rev-parse --git-common-dir").count(), 1, "{calls}");
    assert_eq!(calls.lines().filter(|l| *l == "cat-file --batch-command").count(), 1, "{calls}");
    assert_eq!(calls.lines().filter(|l| l.starts_with("rev-list ")).count(), 1, "{calls}");
    assert!(!calls.lines().any(|l| l.starts_with("show ")), "{calls}");
    let missing = record(&root, "proofs", json!({"goal":"g","instrument":"cover-a","commit":"0".repeat(40),"exit_code":1,"output_sha256":hash(b""),"output_tail":"","ran_at":99}));
    let out = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]).output().unwrap();
    assert_eq!(out.status.code(), Some(1)); assert!(String::from_utf8_lossy(&out.stdout).contains("missing instrument at"));
    assert_eq!(cli(&root, &["show", "g"]), shown); fs::remove_file(missing).unwrap();
    let wrong = record(&root, "proofs", json!({"goal":"g","instrument":"cover-a","commit":wrong_kind,"exit_code":1,"output_sha256":hash(b""),"output_tail":"","ran_at":99}));
    assert_eq!(cli(&root, &["show", "g"]), shown); fs::remove_file(wrong).unwrap();
    let requests = root.join(".whosaidso/cache/requests"); let large = git(&root, &["rev-parse", ":large.dat"]);
    let found = success(command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["grep", "abcd", "--in", "needle.txt"]).env("PATH", format!("{}:{}", bin.display(), std::env::var("PATH").unwrap())).env("PROOF_TRACE", &trace).env("PROOF_GIT", real.trim()).env("PROOF_REQUESTS", &requests).output().unwrap());
    assert!(found.starts_with("needle.txt:1: abcd exact\nnot searched: 1 files"));
    let requests = fs::read_to_string(requests).unwrap(); assert!(requests.contains(&format!("info {}\n", large.trim()))); assert!(!requests.contains(&format!("contents {}\n", large.trim())));
    assert_eq!(cli(&root, &["grep", "abcd", "--in", "needle.txt"]), "needle.txt:1: abcd exact\nnot searched: 1 files (over 1 MiB, binary, not UTF-8, symlink or submodule)\n");
    assert_eq!(cli(&root, &["grep", "😀éxw"]), "needle.txt:3: 😀éxW unicode\nnot searched: 1 files (over 1 MiB, binary, not UTF-8, symlink or submodule)\n");
    assert_eq!(cli(&root, &["grep", "\"needle\""]), "needle.txt:4: quote \"needle\"\nnot searched: 1 files (over 1 MiB, binary, not UTF-8, symlink or submodule)\n");
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("CREATE TABLE sentinel(value); INSERT INTO sentinel VALUES (42);
        DROP TABLE text_fts; CREATE VIRTUAL TABLE text_fts USING fts5(path UNINDEXED,body,tokenize='trigram');
        INSERT INTO text_fts VALUES ('needle.txt','outdated cached content');").unwrap(); drop(db);
    assert_eq!(cli(&root, &["grep", "abcd", "--in", "needle.txt"]), "needle.txt:1: abcd exact\nnot searched: 1 files (over 1 MiB, binary, not UTF-8, symlink or submodule)\n");
    assert_eq!(cli(&root, &["grep", "outdated cached content"]), "grep: no match\nnot searched: 1 files (over 1 MiB, binary, not UTF-8, symlink or submodule)\n");
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    assert_eq!(db.query_row("SELECT value FROM sentinel", [], |r| r.get::<_, i64>(0)).unwrap(), 42); drop(db);
    git(&root, &["add", ".whosaidso"]); git(&root, &["commit", "-m", "evidence"]);
    assert!(cli(&root, &["prune"]).contains("11 proofs")); assert_eq!(cli(&root, &["show", "g"]), shown);
    fs::remove_dir_all(root).unwrap();
}

// File indexing defects exercised through the real binary in one isolated project.
use rusqlite::Connection;
use sha2::{Digest, Sha256};
use std::{
    fs,
    os::unix::fs::{PermissionsExt, symlink},
    path::Path,
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};
fn command(root: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1");
    if program == "git" {
        c.args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
        ]);
    }
    c.args(args);
    c
}
fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    command(root, program, args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let o = run(root, program, args);
    assert!(
        o.status.success(),
        "{args:?}: {}{}",
        String::from_utf8_lossy(&o.stdout),
        String::from_utf8_lossy(&o.stderr)
    );
    String::from_utf8(o.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String {
    ok(root, "git", args)
}
fn cli(root: &Path, args: &[&str]) -> String {
    ok(root, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn write(path: &Path, bytes: &[u8]) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn bounded(root: &Path, args: &[&str]) -> Output {
    let out = run(root, env!("CARGO_BIN_EXE_whosaidso"), args);
    assert!(out.status.success(), "16: {:?}", out);
    out
}
fn stamps(root: &Path) {
    let db = Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    let mut stmt = db.prepare("SELECT f.blob,t.body FROM text_files f JOIN text_fts t ON f.path=t.path WHERE f.path IN ('filtered.rs','race.rs')").unwrap();
    let rows = stmt
        .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))
        .unwrap();
    for row in rows {
        let (stamp, body) = row.unwrap();
        assert_eq!(
            stamp,
            digest(body.as_bytes()),
            "10: stamp must hash indexed bytes"
        );
    }
}
#[test]
fn files_are_safe_exact_and_bounded() {
    let home = std::env::temp_dir().join(format!(
        "files-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    let root = home.join("project ");
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    write(&root.join("localneedle.md"), b"# localheading\n");
    git(&root, &["add", "."]);
    git(&root, &["commit", "-m", "fixture"]);
    // 18: preserve a space at the end of the root, and reject non-UTF-8 roots clearly.
    assert!(
        cli(&root, &["grep", "localheading"]).contains("localneedle.md:1"),
        "18"
    );
    #[cfg(target_os = "linux")]
    {
        use std::os::unix::ffi::OsStringExt;
        let invalid = home.join(std::ffi::OsString::from_vec(b"invalid-\xff".to_vec()));
        fs::create_dir(&invalid).unwrap();
        git(&invalid, &["init", "-q"]);
        let bad = run(
            &invalid,
            env!("CARGO_BIN_EXE_whosaidso"),
            &["grep", "batchneedle"],
        );
        assert_eq!(bad.status.code(), Some(1));
        assert!(
            String::from_utf8_lossy(&bad.stderr).contains("root is not UTF-8"),
            "18: {:?}",
            bad
        );
    }
    // 1 and 17: the disposable cache lives in the git dir, so searching writes nothing into the working tree and nothing can redirect it.
    let records = root.join(".whosaidso");
    let outside = home.join("outside");
    fs::create_dir(&outside).unwrap();
    cli(&root, &["grep", "batchneedle"]);
    assert!(
        !records.exists(),
        "17: searching must not create .whosaidso"
    );
    assert!(root.join(".git/whosaidso/cache/index.db").is_file(), "1");
    assert_eq!(
        git(&root, &["status", "--porcelain", "--ignored"]),
        "",
        "17"
    );
    fs::create_dir_all(&records).unwrap();
    // 14 and 12: records and local files remain searchable without main; partial publications do not.
    write(
        &records.join("phases/p1.json"),
        br#"{"title":"recordneedle","order":1}"#,
    );
    assert!(
        cli(&root, &["find", "localneedle"]).contains("localneedle.md"),
        "14"
    );
    assert!(
        cli(&root, &["find", "recordneedle"]).contains("phase p1"),
        "14"
    );
    write(&records.join("asks/.tmp-publishing"), b"{");
    write(&root.join(".tmp-hiddenneedle.md"), b"# hiddenneedle\n");
    assert!(
        cli(&root, &["find", "hiddenneedle"]).contains("no indexed match"),
        "12"
    );
    // 10: clean filters must not collapse distinct raw working-tree bodies.
    git(&root, &["config", "filter.upper.clean", "tr a-z A-Z"]);
    write(&root.join(".gitattributes"), b"filtered.rs filter=upper\n");
    write(&root.join("filtered.rs"), b"fn hello() {}\n");
    git(&root, &["add", ".gitattributes", "filtered.rs"]);
    git(&root, &["commit", "-m", "filter"]);
    write(&root.join("filtered.rs"), b"fn hello() {} // edit\n");
    cli(&root, &["grep", "batchneedle"]);
    stamps(&root);
    let db = Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    let before: String = db
        .query_row(
            "SELECT blob FROM text_files WHERE path='filtered.rs'",
            [],
            |r| r.get(0),
        )
        .unwrap();
    drop(db);
    write(&root.join("filtered.rs"), b"FN HELLO() {} // EDIT\n");
    cli(&root, &["grep", "batchneedle"]);
    stamps(&root);
    let db = Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    let after: String = db
        .query_row(
            "SELECT blob FROM text_files WHERE path='filtered.rs'",
            [],
            |r| r.get(0),
        )
        .unwrap();
    drop(db);
    assert_ne!(before, after, "10: raw case edits must invalidate stamps");
    // Intercept Git between listing stamps and reading bodies; each indexed buffer hashes itself.
    let real_git = ok(&root, "sh", &["-c", "command -v git"])
        .trim()
        .to_string();
    let bin = home.join("bin");
    fs::create_dir(&bin).unwrap();
    let wrapper = bin.join("git");
    write(&wrapper, b"#!/bin/sh\nif [ \"$1\" = hash-object ]; then\n \"$FILES_REAL_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\n printf 'fn race_b() {}\\n' > race.rs\nelif [ \"$1\" = cat-file ]; then\n printf 'fn race_b() {}\\n' > race.rs\n exec \"$FILES_REAL_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\nelse\n exec \"$FILES_REAL_GIT\" -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main \"$@\"\nfi\n");
    fs::set_permissions(&wrapper, fs::Permissions::from_mode(0o755)).unwrap();
    write(&root.join("race.rs"), b"fn race_a() {}\n");
    let path = format!("{}:{}", bin.display(), std::env::var("PATH").unwrap());
    // Force a changed Git blob so the cat-file hook runs before disk reads.
    git(&root, &["config", "filter.upper.clean", "cat"]);
    write(&root.join("trigger.txt"), b"trigger\n");
    git(&root, &["add", "trigger.txt"]);
    let o = command(
        &root,
        env!("CARGO_BIN_EXE_whosaidso"),
        &["grep", "batchneedle"],
    )
    .env("PATH", &path)
    .env("FILES_REAL_GIT", &real_git)
    .output()
    .unwrap();
    assert!(o.status.success(), "10: {:?}", o);
    stamps(&root);
    assert!(cli(&root, &["grep", "race_b"]).contains("race.rs:1"), "10");
    // 11: Git links, edited/untracked links, ancestor links, and submodules are excluded.
    write(&outside.join("secret.rs"), b"fn outsideneedle() {}\n");
    symlink("outsideneedle-target", root.join("committed.rs")).unwrap();
    write(&root.join("edited.rs"), b"fn original() {}\n");
    write(&root.join("dir/child.rs"), b"fn child() {}\n");
    git(&root, &["add", "committed.rs", "edited.rs", "dir"]);
    git(&root, &["commit", "-m", "links"]);
    fs::remove_file(root.join("edited.rs")).unwrap();
    symlink(outside.join("secret.rs"), root.join("edited.rs")).unwrap();
    symlink(outside.join("secret.rs"), root.join("untracked.rs")).unwrap();
    fs::remove_dir_all(root.join("dir")).unwrap();
    symlink(&outside, root.join("dir")).unwrap();
    let head = git(&root, &["rev-parse", "HEAD"]).trim().to_string();
    git(
        &root,
        &[
            "update-index",
            "--add",
            "--cacheinfo",
            &format!("160000,{head},submodule"),
        ],
    );
    let out = cli(&root, &["grep", "outsideneedle"]);
    assert!(
        out.contains("grep: no match") && out.contains("not searched: 6 files"),
        "11: {out}"
    );
    assert!(
        cli(&root, &["code", "defs", "outsideneedle"]).contains("none recorded"),
        "11"
    );
    // Both queries see additions and deletions on demand.
    cli(&root, &["code", "defs", "fresh"]);
    write(&root.join("fresh.rs"), b"fn fresh() {}\n");
    assert!(cli(&root, &["code", "defs", "fresh"]).contains("fresh.rs:1"));
    assert!(cli(&root, &["grep", "fresh"]).contains("fresh.rs:1"));
    fs::remove_file(root.join("fresh.rs")).unwrap();
    assert!(cli(&root, &["code", "defs", "fresh"]).contains("none recorded"));
    assert!(cli(&root, &["grep", "fresh"]).contains("no match"));
    // 16: every batch persists and queries return the expected answers.
    let mut body = vec![b' '; 1024 * 1024];
    body[..17].copy_from_slice(b"/* batchneedle */");
    for i in 0..160 {
        write(&root.join(format!("batch{i:03}.rs")), &body);
    }
    let o = bounded(&root, &["grep", "batchneedle", "--in", "batch000.rs"]);
    assert!(
        String::from_utf8_lossy(&o.stdout).contains("batch000.rs:1"),
        "16: {:?}",
        o
    );
    let o = bounded(&root, &["code", "defs", "fresh"]);
    assert!(
        String::from_utf8_lossy(&o.stdout).contains("none recorded"),
        "16: {:?}",
        o
    );
    assert!(
        cli(&root, &["grep", "batchneedle", "--in", "batch000.rs"]).contains("batch000.rs:1"),
        "16: all batches must persist"
    );
    let db = Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    for table in ["files", "text_files"] {
        assert_eq!(
            db.query_row(
                &format!("SELECT count(*) FROM {table} WHERE path GLOB 'batch*.rs'"),
                [],
                |r| r.get::<_, i64>(0)
            )
            .unwrap(),
            160
        );
    }
    drop(db);
    fs::remove_dir_all(home).unwrap();
}

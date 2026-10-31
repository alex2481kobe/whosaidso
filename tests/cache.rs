mod common;
use common::process::code_rows as cli;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn warm_queries_reuse_bytes_and_still_notice_edits() {
    let f = Fixture::empty();
    let root = &f.root;
    fs::write(root.join("main.rs"), "fn needle() {}\n").unwrap();
    fs::write(root.join("bad.rs"), [0xff, 0xfe]).unwrap();
    fs::write(root.join("guide.md"), "# Needle guide\n").unwrap();
    git(root, &["add", "."]);
    git(root, &["commit", "-m", "fixture"]);
    git(root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    assert!(cli(root, &["code", "defs", "needle"]).contains("syntactic"));
    assert!(cli(root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(root, &["find", "needle", "guide"]).contains("guide.md"));
    // Removing only fixture blobs makes any repeated cat-file read fail, without using time.
    for path in ["main.rs", "bad.rs", "guide.md"] {
        let blob = git(root, &["rev-parse", &format!(":{path}")]);
        let blob = blob.trim();
        fs::remove_file(root.join(".git/objects").join(&blob[..2]).join(&blob[2..])).unwrap();
    }
    assert!(cli(root, &["code", "defs", "changed_needle"]).contains("absence is not proven"));
    assert!(cli(root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(root, &["find", "needle", "guide"]).contains("guide.md"));
    assert_eq!(
        cli(root, &["code", "defs", "needle"]),
        "main.rs:1  fn needle  [syntactic]\n"
    );
    assert!(cli(root, &["grep", "needle"]).contains("main.rs:1:fn needle() {}"));
    assert!(cli(root, &["find", "needle", "guide"]).contains("guide.md"));
    fs::write(root.join("main.rs"), "fn changed_needle() {}\n").unwrap();
    assert!(cli(root, &["code", "defs", "changed_needle"]).contains("syntactic"));
    assert!(cli(root, &["grep", "needle"]).contains("main.rs:1"));
    assert!(cli(root, &["find", "needle", "guide"]).contains("guide.md"));
    assert_eq!(
        cli(root, &["code", "defs", "changed_needle"]),
        "main.rs:1  fn changed_needle  [syntactic]\n"
    );
}

#[test]
fn concurrent_index_writers_wait_for_sqlite_and_publish_current_files() {
    let f = Fixture::empty();
    let root = &f.root;

    use std::process::Stdio;
    use std::time::Duration;
    write(&root.join("file.txt"), "before");
    cli(root, &["grep", "before"]);
    write(&root.join("file.txt"), "changed");
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("BEGIN IMMEDIATE").unwrap();
    let mut children: Vec<_> = (0..2)
        .map(|_| {
            command(root, BIN, &["grep", "changed"])
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap()
        })
        .collect();
    std::thread::sleep(Duration::from_millis(50));
    for c in &mut children {
        assert!(c.try_wait().unwrap().is_none());
    }
    db.execute_batch("COMMIT").unwrap();
    drop(db);
    for c in children {
        assert!(success(c.wait_with_output().unwrap()).contains("file.txt:1:changed"));
    }
}

#[test]
fn clean_filters_do_not_collapse_raw_content_stamps() {
    let f = Fixture::empty();
    let root = &f.root;

    git(root, &["config", "filter.upper.clean", "tr a-z A-Z"]);
    write(&root.join(".gitattributes"), "filtered.rs filter=upper\n");
    write(&root.join("filtered.rs"), "fn hello() {}\n");
    commit(root);
    let mut stamps = Vec::new();
    for body in ["fn hello() {} // edit\n", "FN HELLO() {} // EDIT\n"] {
        write(&root.join("filtered.rs"), body);
        cli(root, &["grep", "hello"]);
        let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
        let (stamp,text):(String,String)=db.query_row("SELECT f.blob,t.body FROM text_files f JOIN text_fts t ON f.path=t.path WHERE f.path='filtered.rs'",[],|r|Ok((r.get(0)?,r.get(1)?))).unwrap();
        assert_eq!(stamp, hash(text.as_bytes()));
        stamps.push(stamp);
    }
    assert_ne!(stamps[0], stamps[1]);
}

#[test]
fn obsolete_cache_tables_rebuild_without_preserving_stale_answers() {
    let f = Fixture::empty();
    let root = &f.root;
    write(&root.join("dir.txt"), "replacementneedle");
    cli(root, &["grep", "replacementneedle"]);
    let path = root.join(".git/whosaidso/cache/index.db");
    let db = rusqlite::Connection::open(&path).unwrap();
    db.execute_batch("PRAGMA user_version=1; UPDATE text_fts SET body='stalecacheneedle' WHERE path='dir.txt'; CREATE TABLE obsolete(value);").unwrap();
    drop(db);
    assert!(cli(root, &["grep", "stalecacheneedle"]).contains("no match"));
    assert!(cli(root, &["grep", "replacementneedle"]).contains("dir.txt:1"));
    let db = rusqlite::Connection::open(&path).unwrap();
    assert_eq!(
        db.query_row("PRAGMA user_version", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        2
    );
    assert_eq!(
        db.query_row(
            "SELECT count(*) FROM sqlite_master WHERE name='obsolete'",
            [],
            |r| r.get::<_, i64>(0)
        )
        .unwrap(),
        0
    );
    db.execute_batch("CREATE TABLE sentinel(value);INSERT INTO sentinel VALUES(42);DROP TABLE text_fts;CREATE VIRTUAL TABLE text_fts USING fts5(path UNINDEXED,body,tokenize='trigram');INSERT INTO text_fts VALUES('dir.txt','outdated cached content');").unwrap();
    drop(db);
    assert!(cli(root, &["grep", "outdated cached content"]).contains("no match"));
    assert!(cli(root, &["grep", "replacementneedle"]).contains("dir.txt:1"));
    let db = rusqlite::Connection::open(path).unwrap();
    assert_eq!(
        db.query_row("SELECT value FROM sentinel", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        42
    );
}

mod common;
use common::process::*;
use common::*;

#[test]
fn syntax_and_text_stamp_the_buffer_they_actually_read() {
    let f = Fixture::empty();
    let root = &f.root;

    use common::scip::*;
    write(&root.join("race.rs"), "fn base() {}\n");
    commit(root);
    write(&root.join("race.rs"), "fn race_a() {}\n");
    import(
        root,
        &[doc(
            "race.rs",
            "scip-test pkg project 1 race_a().",
            "",
            &[(&[0, 3, 9], true)],
        )],
    );
    write(&root.join("trigger.rs"), "fn trigger() {}");
    git(root, &["add", "trigger.rs"]);
    let real = success(
        command(root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = root.join(".git/bin");
    executable(
        &bin.join("git"),
        "#!/bin/sh\nif test \"$1\" = cat-file; then printf 'fn race_b() {}\\n' > race.rs; fi\nexec \"$REAL_GIT\" \"$@\"\n",
    );
    let path = format!("{}:{}", bin.display(), std::env::var("PATH").unwrap());
    assert_eq!(
        success(
            command(root, BIN, &["code", "defs", "race_b"])
                .env("PATH", &path)
                .env("REAL_GIT", real.trim())
                .output()
                .unwrap()
        ),
        "race.rs:1  fn race_b  [syntactic]\n"
    );
    assert!(cli(root, &["grep", "race_b"]).contains("race.rs:1"));
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    let (stamp,body):(String,String)=db.query_row("SELECT f.blob,t.body FROM text_files f JOIN text_fts t ON f.path=t.path WHERE f.path='race.rs'",[],|r|Ok((r.get(0)?,r.get(1)?))).unwrap();
    assert_eq!(stamp, hash(body.as_bytes()));
}

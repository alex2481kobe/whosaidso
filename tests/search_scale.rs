mod common;
use common::process::*;
use common::*;

#[test]
#[ignore = "manual: indexing batch boundary"]
fn multiple_batches_persist_every_file() {
    let f = Fixture::empty();
    let root = &f.root;

    let body = format!("/* batchneedle */{}", " ".repeat(1024 * 1024 - 17));
    for n in 0..65 {
        write(&root.join(format!("batch{n:03}.rs")), &body);
    }
    assert!(cli(root, &["grep", "batchneedle", "--in", "batch000.rs"]).contains("batch000.rs:1"));
    cli(root, &["code", "defs", "fresh"]);
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    for table in ["files", "text_files"] {
        assert_eq!(
            db.query_row(
                &format!("SELECT count(*) FROM {table} WHERE path GLOB 'batch*.rs'"),
                [],
                |r| r.get::<_, i64>(0)
            )
            .unwrap(),
            65
        );
    }
}

#[test]
#[ignore = "manual: bounded memory under a large result set"]
fn every_code_collector_bounds_memory_on_large_result_sets() {
    let f = Fixture::empty();
    let root = &f.root;
    write(&root.join("many.rs"), "fn repeated() {}\n");
    cli(root, &["code", "defs", "repeated"]);
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("DELETE FROM defs; WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<400000) INSERT INTO defs SELECT 'many.rs','repeated','fn',i,'repeated' FROM n; INSERT INTO calls SELECT path,name,line,'caller',qualified FROM defs; INSERT INTO includes SELECT path,'target.h',line FROM defs;").unwrap();
    drop(db);
    for query in [
        "defs repeated",
        "refs repeated",
        "deps many.rs",
        "includers target.h",
    ] {
        let out = success(
            command(
                root,
                "sh",
                &[
                    "-c",
                    &format!("ulimit -v 45000; exec \"$1\" code {query}"),
                    "sh",
                    BIN,
                ],
            )
            .output()
            .unwrap(),
        );
        assert_eq!(out.lines().count(), 21);
        assert!(out.ends_with("shown 20 of 400000\n"));
    }
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("INSERT INTO scip_docs SELECT path,blob FROM files WHERE path='many.rs'; INSERT INTO scip_syms VALUES ('scip-test pkg project 1 repeated().','repeated','repeated'); INSERT INTO scip_occ SELECT path,'scip-test pkg project 1 repeated().',line,1 FROM defs;").unwrap();
    drop(db);
    let text = success(
        command(
            root,
            "sh",
            &[
                "-c",
                "ulimit -v 45000; exec \"$1\" code defs repeated",
                "sh",
                BIN,
            ],
        )
        .output()
        .unwrap(),
    );
    assert!(text.ends_with("shown 20 of 400000\n"));
    assert!(text.lines().take(20).all(|l| l.ends_with("[precise]")));
}

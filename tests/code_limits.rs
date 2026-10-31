mod common;
use common::process::*;
use common::*;

#[test]
fn code_collectors_bound_all_answers_and_keep_exact_counts() {
    let f = Fixture::empty();
    let root = &f.root;

    write(&root.join("many.rs"), "fn repeated() {}\n");
    cli(root, &["code", "defs", "repeated"]);
    let db = rusqlite::Connection::open(root.join(".git/whosaidso/cache/index.db")).unwrap();
    db.execute_batch("DELETE FROM defs; WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<25) INSERT INTO defs SELECT 'many.rs','repeated','fn',i,'repeated' FROM n; INSERT INTO calls SELECT path,name,line,'caller',qualified FROM defs; INSERT INTO includes SELECT path,'target.h',line FROM defs;").unwrap();
    drop(db);
    for args in [
        &["code", "defs", "repeated"][..],
        &["code", "refs", "repeated"],
        &["code", "deps", "many.rs"],
        &["code", "includers", "target.h"],
    ] {
        let text = cli(root, args);
        assert_eq!(text.lines().count(), 21);
        assert!(text.ends_with("shown 20 of 25\n"));
    }
}

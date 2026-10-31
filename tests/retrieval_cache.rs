mod common;
use common::{Fixture, process::cli};
use rusqlite::Connection;
use std::fs;

#[test]
fn extractor_upgrade_rebuilds_retrieval_templates() {
    let f = Fixture::sources(&[("one.rs", "fn needle() {}\n")]);
    assert!(cli(&f.root, &["code", "defs", "needle"]).contains("one.rs:1"));
    let facts = Connection::open(f.root.join(".git/whosaidso/code/facts.db")).unwrap();
    // A previous extractor did not recognize this definition.
    facts.execute("DELETE FROM syntax_defs", []).unwrap();
    assert!(cli(&f.root, &["find", "needle", "kind:symbol"]).contains("no indexed match"));
    facts.execute_batch("UPDATE facts SET extractor='previous:'||extractor; UPDATE generations SET stamp='previous:'||stamp; UPDATE manifests SET stamp='previous:'||stamp;").unwrap();
    let search = Connection::open(f.root.join(".git/whosaidso/search/search.db")).unwrap();
    // Give the warmed retrieval rows the previous extractor's identity too.
    let version: String = facts
        .query_row(
            "SELECT substr(extractor,10,instr(substr(extractor,10),':')-1) FROM facts LIMIT 1",
            [],
            |r| r.get(0),
        )
        .unwrap();
    search
        .execute(
            "UPDATE stamps SET stamp=replace(stamp,?1,'previous')",
            [&version],
        )
        .unwrap();
    search
        .execute(
            "UPDATE blob_templates SET address=replace(address,?1,'previous')",
            [&version],
        )
        .unwrap();
    drop(search);
    drop(facts);
    assert!(cli(&f.root, &["code", "defs", "needle"]).contains("one.rs:1"));
    let warm = cli(&f.root, &["find", "needle", "kind:symbol"]);
    let search = f.root.join(".git/whosaidso/search");
    fs::rename(&search, search.with_file_name("previous-search")).unwrap();
    let clean = cli(&f.root, &["find", "needle", "kind:symbol"]);
    assert!(
        clean.contains("symbol one.rs:1:needle [syntactic]"),
        "{clean}"
    );
    assert_eq!(
        warm, clean,
        "warm retrieval differs after extractor upgrade"
    );
}

mod common;
use common::process::*;
use common::*;
#[test]
fn narrow_flow_drift_checks_do_not_read_outside_covers() {
    let f = Fixture::new();
    write(&f.root.join("covered.rs"), "fn entry() {}\n");
    let outside = "fn unrelated_outside_cover() {}\n";
    write(&f.root.join("outside.rs"), outside);
    commit(&f.root);
    let plan = serde_json::json!({"covers":{"paths":["covered.rs"]},"nodes":[
        {"id":"entry","type":"function","paths":["covered.rs"],"symbols":["entry"]}
    ],"steps":[]});
    cli(&f.root, &["flow", "put", "narrow", &plan.to_string()]);
    cli(&f.root, &["flow", "drift-check"]);
    let db = rusqlite::Connection::open_with_flags(
        f.root.join(".git/whosaidso/code/facts.db"),
        rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY,
    )
    .unwrap();
    let read: i64 = db
        .query_row(
            "SELECT count(*) FROM facts WHERE body=?1",
            [outside.as_bytes()],
            |row| row.get(0),
        )
        .unwrap();
    assert_eq!(
        read, 0,
        "landing drift check read source outside its narrow cover"
    );
}

#[test]
fn unread_name_candidates_cannot_become_a_false_pass() {
    use common::flow::FlowFixture;
    let f = Fixture::sources(&[
        ("app.rs", "fn entry() { same(); }\nfn same() {}\n"),
        ("api.rs", "fn same() {}\n"),
        ("outside.rs", "fn same() {}\n"),
    ]);
    f.write(
        "app",
        &serde_json::json!({"covers":{"paths":["app.rs","api.rs"]},"nodes":[
        {"id":"entry","type":"function","paths":["app.rs"]},
        {"id":"api","type":"function","paths":["api.rs"]}
    ],"steps":[]}),
    );
    bad(&f.root, &["flow", "drift-check"], "app ");
    let comparison: serde_json::Value =
        serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_ne!(comparison["state"], "built");
}

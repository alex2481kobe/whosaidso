mod common;
use common::{process::cli, write, Fixture};

#[test]
fn long_search_labels_round_trip_into_code_show() {
    let f = Fixture::empty();
    let name = format!("needle{}", "x".repeat(600));
    write(&f.root.join("long.rs"), format!("fn {name}() {{}}\n"));
    let found = cli(&f.root, &["find", "kind:symbol", &name]);
    let selector = found.lines().next().unwrap();
    let body = cli(&f.root, &["code", "show", selector]);
    assert!(body.contains(&format!("fn {name}() {{}}")), "{body}");
    assert!(selector.contains("long.rs:1"), "{selector}");
    assert!(!selector.contains("truncated"), "{selector}");
}

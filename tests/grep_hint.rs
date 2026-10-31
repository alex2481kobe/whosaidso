mod common;
use common::{
    BIN, Fixture,
    process::{command, success},
};

#[test]
fn missing_literal_identifier_points_to_search_and_definitions() {
    let f = Fixture::sources(&[("one.c", "void available(void) {}\n")]);
    let out = command(&f.root, BIN, &["grep", "free_nsproxy"])
        .output()
        .unwrap();
    let hint = String::from_utf8_lossy(&out.stderr);
    assert!(
        hint.contains("find free_nsproxy") && hint.contains("code defs free_nsproxy"),
        "{hint}"
    );
    assert_eq!(hint.lines().count(), 1);
    assert_eq!(success(out), "grep: no matches\n");
    for args in [
        &["grep", "missing text"][..],
        &["grep", "--regex", "^missing$"],
    ] {
        let out = command(&f.root, BIN, args).output().unwrap();
        assert!(!String::from_utf8_lossy(&out.stderr).contains("hint:"));
        assert_eq!(success(out), "grep: no matches\n");
    }
}

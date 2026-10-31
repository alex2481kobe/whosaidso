mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;

#[test]
fn questions_and_children_block_done_but_a_childless_milestone_needs_only_evidence() {
    let f = Fixture::new();
    let root = &f.root;
    r::proof(root, &head(root), 1, 0);
    commit(root);
    assert!(cli(root, &["goals"]).contains("done    g"));
    let note = root.join(".whosaidso/notes/why.md");
    write(
        &note,
        "---\n{\"type\":\"question\",\"status\":\"open\",\"title\":\"Why?\",\"goal\":\"g\"}\n---\nUnanswered.\n",
    );
    assert!(!cli(root, &["goals"]).contains("done    g"));
    cli(
        root,
        &[
            "note",
            "resolve",
            "why",
            &hash(&std::fs::read(&note).unwrap()),
        ],
    );
    let path = r::goal(root, "child");
    let mut child = value(&path);
    child["parent"] = json!("g");
    r::put(root, "goals", "child", &child);
    commit(root);
    assert!(!cli(root, &["goals"]).contains("done    g"));
    let proof = r::proof(root, &head(root), 2, 0);
    let mut p = value(&proof);
    p["goal"] = json!("child");
    r::put(root, "proofs", "", &p);
    commit(root);
    assert!(cli(root, &["goals"]).contains("done    g"));
    r::clear(root, "proofs");
    r::put(root, "proofs", "", &p);
    commit(root);
    let out = cli(root, &["goals"]);
    assert!(out.contains("done    child"));
    assert!(!out.contains("done    g"));
}

#[test]
fn check_changes_are_marked_against_the_first_main_record() {
    let f = Fixture::new();
    let root = &f.root;
    r::check_command(root, &["false"]);
    commit(root);
    assert!(cli(root, &["goals"]).contains("check changed"));
    assert!(cli(root, &["show", "g"]).contains("check changed"));
    r::check_command(root, &["true"]);
    assert!(!cli(root, &["goals"]).contains("check changed"));
}

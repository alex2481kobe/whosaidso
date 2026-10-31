mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;

#[test]
fn folding_keeps_verdicts_references_search_and_revision_checked_reopen() {
    let f = Fixture::new();
    let root = &f.root;
    let dependent = r::goal(root, "later");
    let mut g = value(&dependent);
    g["after"] = json!(["g"]);
    r::put(root, "goals", "later", &g);
    r::proof(root, &head(root), 1, 0);
    write(
        &root.join(".whosaidso/notes/result.md"),
        "---\n{\"type\":\"claim\",\"status\":\"resolved\",\"title\":\"Measured result\",\"goal\":\"g\"}\n---\nA measurable outcome.\n",
    );
    write(
        &root.join(".whosaidso/notes/design.md"),
        "---\n{\"type\":\"decision\",\"status\":\"resolved\",\"title\":\"Design\",\"goal\":\"g\"}\n---\nKeep live.\n",
    );
    let image = attach(root, "measured figure");
    let image_bytes = std::fs::read(&image).unwrap();
    commit(root);
    let before = cli(root, &["goals"]);
    cli(root, &["prune"]);
    let bundle = root.join(".whosaidso/done/g.jsonl");
    assert!(bundle.is_file());
    bad(root, &["prove", "g"], "reopen");
    bad(
        root,
        &["evidence", "add", "g", "missing", "Changed result"],
        "reopen",
    );
    assert!(!root.join(".whosaidso/goals/g.json").exists());
    assert!(root.join(".whosaidso/notes/design.md").is_file());
    assert_eq!(std::fs::read(&image).unwrap(), image_bytes);
    assert!(cli(root, &["show", "g"]).contains("attached: measured figure"));
    assert_eq!(cli(root, &["goals"]), before);
    cli(root, &["check"]);
    assert!(cli(root, &["find", "measurable", "outcome"]).contains("result"));
    assert!(cli(root, &["note", "show", "result"]).contains("measurable"));
    commit(root);
    let rev = hash(&std::fs::read(&bundle).unwrap());
    let stale = f.worktree("stale");
    r::goal(&stale, "g");
    commit(&stale);
    assert!(bad(&stale, &["gate"], "reopen").contains("g"));
    bad(
        root,
        &["reopen", "g", &"0".repeat(64)],
        "destination revision",
    );
    cli(root, &["reopen", "g", &rev]);
    assert!(root.join(".whosaidso/goals/g.json").is_file());
    assert!(!bundle.exists());
    assert_eq!(cli(root, &["goals"]), before);
    commit(root);
    cli(root, &["gate"]);
}

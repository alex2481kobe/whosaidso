mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::fs;

#[test]
fn goals_allow_optional_checks_and_reject_all_bad_references() {
    let f = Fixture::new();
    let root = &f.root;
    assert!(cli(root, &["check"]).contains("1 goals"));
    let mut g = value(&root.join(".whosaidso/goals/g.json"));
    g.as_object_mut().unwrap().remove("check");
    r::put(root, "goals", "g", &g);
    cli(root, &["check"]);
    g["parent"] = json!("missing");
    g["after"] = json!(["other"]);
    g["done_when"] = json!(" ");
    r::put(root, "goals", "g", &g);
    let out = bad(root, &["check"], "empty done_when");
    assert!(out.contains("missing") && out.contains("other"));
    assert!(bad(root, &["goals"], "broken  g").contains("0/1 done"));
}

#[test]
fn cycles_reserved_names_and_unknown_fields_are_broken() {
    let f = Fixture::new();
    let root = &f.root;
    let mut g = value(&root.join(".whosaidso/goals/g.json"));
    g["parent"] = json!("g");
    r::put(root, "goals", "g", &g);
    bad(root, &["check"], "dependency chain loops");
    r::clear(root, "goals");
    r::goal(root, "none");
    bad(root, &["check"], "reserved goal id none");
    g["extra"] = json!(true);
    r::put(root, "goals", "g", &g);
    bad(root, &["goals"], "unknown field");
}

#[test]
fn record_size_hash_names_and_proof_tail_are_checked() {
    let f = Fixture::new();
    let root = &f.root;
    let path = root.join(".whosaidso/goals/g.json");
    let mut bytes = fs::read(&path).unwrap();
    bytes.resize(65536, b' ');
    write(&path, &bytes);
    cli(root, &["check"]);
    bytes.push(b' ');
    write(&path, bytes);
    bad(root, &["check"], "record file cap 65536 bytes: count 65537");
    r::goal(root, "g");
    let p = r::proof(root, &head(root), 1, 0);
    let mut v = value(&p);
    fs::remove_file(p).unwrap();
    v["output_tail"] = json!("x".repeat(16385));
    let p = r::put(root, "proofs", "", &v);
    bad(root, &["check"], "output tail cap 16384 bytes: count 16385");
    fs::rename(&p, p.with_file_name("wrong.json")).unwrap();
    bad(root, &["goals"], "proof filename");
}

#[test]
fn broken_check_stays_in_progress_and_invalid_json_is_visible() {
    let f = Fixture::new();
    let root = &f.root;
    r::goal(root, "h");
    commit(root);
    r::proof(root, &head(root), 1, 0);
    commit(root);
    let mut h = value(&root.join(".whosaidso/goals/h.json"));
    h["check"] = json!(["nonexistent-command-for-fixture"]);
    r::put(root, "goals", "h", &h);
    let out = bad(root, &["goals"], "1/2 done");
    assert!(out.contains("broken  h"));
    write(&root.join(".whosaidso/goals/h.json"), "{");
    assert!(bad(root, &["goals"], "broken  h").contains("1/2 done"));
}

#[test]
fn temporary_publications_are_ignored_and_symlink_directories_refused() {
    let f = Fixture::new();
    let root = &f.root;
    write(&root.join(".whosaidso/proofs/.tmp-writing"), "{");
    cli(root, &["check"]);
    fs::remove_dir_all(root.join(".whosaidso/proofs")).unwrap();
    std::os::unix::fs::symlink(root.join(".git"), root.join(".whosaidso/proofs")).unwrap();
    bad(root, &["prove", "g"], "symlink");
}

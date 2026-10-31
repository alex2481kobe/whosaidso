mod common;
use common::process::*;
use common::{records as r, *};

#[test]
fn unlanded_evidence_never_completes_a_goal() {
    for attached in [true, false] {
        let f = Fixture::new();
        let branch = f.worktree("worker");
        if attached {
            attach(&branch, "uncommitted report");
        } else {
            r::proof(&branch, &head(&branch), 1, 0);
        }
        assert!(!cli(&branch, &["goals"]).contains("done    g"));
        commit(&branch);
        assert!(!cli(&branch, &["goals"]).contains("done    g"));
        git(&f.root, &["merge", "-q", "worker"]);
        assert!(cli(&f.root, &["goals"]).contains("done    g"));
        cli(&f.root, &["prune"]);
        commit(&f.root);
        assert!(cli(&f.root, &["goals"]).contains("done    g"));
    }
}

#[test]
fn a_bad_bundle_does_not_hide_other_children_or_live_questions() {
    let f = Fixture::new();
    for (id, parent) in [
        ("g", None),
        ("child", Some("g")),
        ("bad", None),
        ("bad-child", Some("bad")),
    ] {
        let p = r::goal(&f.root, id);
        let mut v = value(&p);
        v["parent"] = serde_json::json!(parent);
        r::put(&f.root, "goals", id, &v);
    }
    commit(&f.root);
    let p = r::proof(&f.root, &head(&f.root), 1, 0);
    let mut proof = value(&p);
    for id in ["child", "bad", "bad-child"] {
        proof["goal"] = serde_json::json!(id);
        r::put(&f.root, "proofs", "", &proof);
    }
    commit(&f.root);
    cli(&f.root, &["prune"]);
    commit(&f.root);
    r::goal(&f.root, "live");
    proof["goal"] = serde_json::json!("live");
    // Record the live goal before evidence refers to its revision.
    commit(&f.root);
    proof["commit"] = serde_json::json!(head(&f.root));
    r::put(&f.root, "proofs", "", &proof);
    write(
        &f.root.join(".whosaidso/notes/question.md"),
        "---\n{\"type\":\"question\",\"status\":\"open\",\"title\":\"Unanswered\",\"goal\":\"live\"}\n---\nNeeds an answer.\n",
    );
    commit(&f.root);
    write(&f.root.join(".whosaidso/done/bad.jsonl"), "malformed\n");
    let out = command(&f.root, BIN, &["goals"]).output().unwrap();
    let text = String::from_utf8(out.stdout).unwrap();
    assert!(text.contains("child"), "{text}");
    assert!(!text.contains("done    live"), "{text}");
    assert!(cli(&f.root, &["note", "show", "question"]).contains("Needs an answer"));
}

#[test]
fn landed_work_without_evidence_keeps_its_claim_exclusive() {
    let f=Fixture::new();
    cli(&f.root,&["install-hooks"]); commit(&f.root);
    work(&f.root,"implementation","g");
    success(command(&f.root,BIN,&["start","g"]).env("WHOSAIDSO_AGENT","first").output().unwrap());
    for args in [vec!["start","g"],vec!["next"]] {
        failure(command(&f.root,BIN,&args).env("WHOSAIDSO_AGENT","second").output().unwrap(),"claimed");
    }
}

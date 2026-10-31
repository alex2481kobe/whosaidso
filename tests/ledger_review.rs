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

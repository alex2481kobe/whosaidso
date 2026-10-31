mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn landing_refuses_a_symlinked_flow_record() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    // The target lives in Git's private directory, outside the published plan.
    let git_dir = git(&lane, &["rev-parse", "--absolute-git-dir"]);
    let plan = std::path::Path::new(git_dir.trim()).join("plan.json");
    write(
        &plan,
        json!({"covers":{"paths":["future.rs"]},"nodes":[],"steps":[]}).to_string(),
    );
    std::fs::create_dir_all(lane.join(".whosaidso/flows")).unwrap();
    std::os::unix::fs::symlink(&plan, lane.join(".whosaidso/flows/app.json")).unwrap();
    commit(&lane);
    let before = git(&lane, &["ls-remote", "origin", "refs/heads/main"]);
    bad(
        &lane,
        &["land", "--reason", "unchanged behavior"],
        "flow is not a regular file",
    );
    assert_eq!(
        before,
        git(&lane, &["ls-remote", "origin", "refs/heads/main"])
    );
}

#[test]
fn landing_refuses_regression_of_unchanged_destination_anchors() {
    let f = Fixture::new();
    write(&f.root.join("app.rs"), "fn old() {}\n");
    let mut plan = json!({"covers":{"paths":["app.rs"]},"nodes":[{"id":"entry","type":"function","symbols":["old"]}],"steps":[]});
    f.write("app", &plan);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    write(&lane.join("app.rs"), "fn renamed() {}\n");
    commit(&lane);
    let before = git(&lane, &["ls-remote", "origin", "refs/heads/main"]);
    bad(
        &lane,
        &["land", "--reason", "unchanged behavior"],
        "app drifted",
    );
    assert_eq!(
        before,
        git(&lane, &["ls-remote", "origin", "refs/heads/main"])
    );
    plan["nodes"][0]["symbols"] = json!(["renamed"]);
    write(&lane.join(".whosaidso/flows/app.json"), plan.to_string());
    commit(&lane);
    cli(&lane, &["land", "--reason", "updated plan"]);
}

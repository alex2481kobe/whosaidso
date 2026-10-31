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

#[test]
fn name_only_calls_stay_in_their_language_family() {
    let f = Fixture::sources(&[
        ("app.rs", "fn screen() { verify(); }\n"),
        ("api.py", "def verify(): pass\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs","api.py"]},"nodes":[
        {"id":"screen","type":"function","paths":["app.rs"],"symbols":["screen"]},
        {"id":"api","type":"function","paths":["api.py"],"symbols":["verify"]}
    ],"steps":[{"id":"call","from":"screen","to":"api","when":"ready"}]}),
    );
    let report: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_ne!(report["steps"]["call"]["state"], "built");
    write(&f.root.join("web.js"), "function web() { save(); }\n");
    write(&f.root.join("save.ts"), "export function save() {}\n");
    f.write("web", &json!({"covers":{"paths":["web.js","save.ts"]},"nodes":[
        {"id":"web","type":"function","paths":["web.js"]}, {"id":"save","type":"function","paths":["save.ts"]}
    ],"steps":[{"id":"call","from":"web","to":"save","when":"ready"}]}));
    let report: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "web"])).unwrap();
    assert_eq!(report["steps"]["call"]["state"], "built");
}

#[test]
fn landing_refuses_covered_files_missing_from_the_manifest() {
    for ignored in [false, true] {
        let f = Fixture::new();
        write(&f.root.join("app.rs"), "fn ready() {}\n");
        f.write("app",&json!({"covers":{"paths":["app.rs","linked.rs","generated/**","future/**"]},"nodes":[{"id":"ready","type":"function","symbols":["ready"]}],"steps":[]}));
        cli(&f.root, &["flow", "drift-check"]);
        commit(&f.root);
        f.remote();
        let lane = f.worktree("worker");
        if ignored {
            write(&lane.join(".gitignore"), "generated/\n");
            write(&lane.join("generated/hidden.rs"), "fn hidden() {}\n");
        } else {
            let dir = git(&lane, &["rev-parse", "--absolute-git-dir"]);
            let target = std::path::Path::new(dir.trim()).join("hidden.rs");
            write(&target, "fn hidden() {}\n");
            std::os::unix::fs::symlink(target, lane.join("linked.rs")).unwrap();
        }
        commit(&lane);
        let before = git(&lane, &["ls-remote", "origin", "refs/heads/main"]);
        bad(
            &lane,
            &["land", "--reason", "unchanged behavior"],
            "app unknown",
        );
        assert_eq!(
            before,
            git(&lane, &["ls-remote", "origin", "refs/heads/main"])
        );
    }
}

#[test]
fn authoring_refuses_a_flow_that_committed_reads_cannot_load() {
    let f = Fixture::new();
    let mut plan = json!({"covers":{"paths":["future.rs"]},"nodes":[{"id":"future","type":"function"}],"steps":[]});
    cli(&f.root, &["flow", "put", "app", &plan.to_string()]);
    let file = f.root.join(".whosaidso/flows/app.json");
    let before = std::fs::read(&file).unwrap();
    plan["nodes"][0]["note"] = json!("x".repeat(65536));
    bad(
        &f.root,
        &["flow", "put", "app", &plan.to_string()],
        "record file cap",
    );
    assert_eq!(std::fs::read(file).unwrap(), before);
    commit(&f.root);
    cli(&f.root, &["flow", "view", "app", "--branch", "main"]);
}

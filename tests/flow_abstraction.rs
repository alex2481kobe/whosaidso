mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

fn compare(f: &Fixture) -> Value {
    serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap()
}

fn plan() -> Value {
    json!({"covers":{"paths":["src/**"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client/**"]},
        {"id":"server","type":"part","paths":["src/server/**"]}
    ],"steps":[]})
}

#[test]
fn crossings_are_counted_per_box_and_new_files_inherit_ownership() {
    let f = Fixture::sources(&[
        (
            "src/client/a.rs",
            "fn entry() { save(); save(); external_api(); }\n",
        ),
        ("src/server/b.rs", "fn save() {}\n"),
    ]);
    let mut plan = plan();
    f.write("app", &plan);
    let v = compare(&f);
    assert_eq!(v["state"], "unplanned");
    assert_eq!(v["unplanned"].as_array().unwrap().len(), 1);
    assert_eq!(v["unplanned"][0]["from"], "client");
    assert_eq!(v["unplanned"][0]["to"], "server");
    assert_eq!(v["unplanned"][0]["count"], 2);
    assert_eq!(v["unplanned"][0]["evidence"], "syntactic");
    plan["steps"] = json!([{"id":"save","from":"client","to":"server","when":"ready"}]);
    f.write("app", &plan);
    assert_eq!(compare(&f)["state"], "built");
    cli(&f.root, &["flow", "drift-check"]);
    write(
        &f.root.join("src/unowned.rs"),
        "fn extra() { external_api(); }\n",
    );
    let v = compare(&f);
    assert_eq!(v["state"], "built");
    assert_eq!(v["unowned"][0]["path"], "src/unowned.rs");
    cli(&f.root, &["flow", "drift-check"]);
    write(
        &f.root.join("src/server/new.rs"),
        "fn reverse() { entry(); }\n",
    );
    let v = compare(&f);
    assert_eq!(v["unplanned"][0]["from"], "server");
    assert_eq!(v["unplanned"][0]["to"], "client");
    bad(&f.root, &["flow", "drift-check"], "unplanned");
}

#[test]
fn name_ambiguity_is_judged_between_boxes_and_never_passes() {
    let f = Fixture::sources(&[
        ("src/client/a.rs", "fn entry() { save(); }\n"),
        ("src/server/a.rs", "fn save() {}\n"),
        ("src/server/b.rs", "fn save() {}\n"),
    ]);
    let mut plan = plan();
    plan["steps"] = json!([{"id":"save","from":"client","to":"server","when":"ready"}]);
    f.write("app", &plan);
    assert_eq!(compare(&f)["state"], "built");
    write(&f.root.join("src/client/other.rs"), "fn save() {}\n");
    let v = compare(&f);
    assert_eq!(v["steps"]["save"]["state"], "ambiguous");
    assert_eq!(v["unplanned"][0]["state"], "unknown");
    bad(&f.root, &["flow", "drift-check"], "ambiguous");
}

#[test]
fn path_function_anchors_resolve_and_parent_arrows_cover_children() {
    let f = Fixture::sources(&[
        ("src/client/a.rs", "fn entry() { save(); }\n"),
        ("src/server/b.rs", "fn save() {}\n"),
    ]);
    let mut plan = plan();
    plan["nodes"].as_array_mut().unwrap().extend([
        json!({"id":"entry","type":"function","parent":"client","symbols":["src/client/a.rs::entry"]}),
        json!({"id":"save-function","type":"function","parent":"server","symbols":["src/server/b.rs::save"]}),
    ]);
    plan["steps"] = json!([{"id":"save","from":"client","to":"server","when":"ready"}]);
    f.write("app", &plan);
    assert_eq!(compare(&f)["state"], "built");
}

#[test]
fn landing_still_refuses_a_removed_planned_call() {
    let f = Fixture::new();
    write(&f.root.join("src/client/a.rs"), "fn entry() { save(); }\n");
    write(&f.root.join("src/server/b.rs"), "fn save() {}\n");
    let mut plan = plan();
    plan["steps"] = json!([{"id":"save","from":"client","to":"server","when":"ready"}]);
    f.write("app", &plan);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    write(
        &lane.join("src/client/a.rs"),
        "fn entry() { external_api(); }\n",
    );
    commit(&lane);
    let before = git(&lane, &["ls-remote", "origin", "refs/heads/main"]);
    bad(&lane, &["land", "--reason", "documentation"], "app drifted");
    assert_eq!(
        before,
        git(&lane, &["ls-remote", "origin", "refs/heads/main"])
    );
}

#[test]
fn comparison_details_are_bounded_without_hiding_the_verdict() {
    let f = Fixture::new();
    let mut nodes = vec![json!({"id":"caller","type":"part","paths":["app.rs"]})];
    let mut calls = String::new();
    for i in 0..25 {
        let path = format!("part{i}.rs");
        write(&f.root.join(&path), format!("fn target{i}() {{}}\n"));
        calls.push_str(&format!("target{i}();"));
        nodes.push(json!({"id":format!("part{i}"),"type":"part","paths":[path]}));
    }
    write(&f.root.join("app.rs"), format!("fn caller() {{{calls}}}\n"));
    f.write(
        "app",
        &json!({"covers":{"paths":["*.rs"]},"nodes":nodes,"steps":[]}),
    );
    let v = compare(&f);
    assert_eq!(v["state"], "unplanned");
    assert_eq!(v["totals"]["unplanned"], 25);
    assert_eq!(v["unplanned"].as_array().unwrap().len(), 20);
    let page: Value =
        serde_json::from_str(&cli(&f.root, &["flow", "view", "app", "", "20"])).unwrap();
    assert_eq!(page["unplanned"].as_array().unwrap().len(), 5);
    bad(&f.root, &["flow", "drift-check"], "unplanned");
}

#[test]
fn documents_and_styles_do_not_invent_code_coverage_gaps() {
    let f = Fixture::sources(&[
        ("app.rs", "fn entry() {}\n"),
        ("README.md", "Text"),
        ("app.css", "body {}"),
    ]);
    f.write("app", &json!({"covers":{"paths":["*"]},"nodes":[{"id":"app","type":"part","paths":["*"]}],"steps":[]}));
    assert_eq!(compare(&f)["state"], "built");
    write(&f.root.join("unknown.xyz"), "unrecognized language");
    assert_eq!(compare(&f)["state"], "unknown");
}

#[test]
fn a_low_level_arrow_covers_the_enclosing_box_pair_once() {
    let f = Fixture::sources(&[
        ("src/client/a.rs", "fn entry() { save(); second(); }\n"),
        ("src/server/b.rs", "fn save() {}\nfn second() {}\n"),
    ]);
    let mut plan = plan();
    plan["nodes"].as_array_mut().unwrap().extend([
        json!({"id":"entry","type":"function","parent":"client","symbols":["entry"]}),
        json!({"id":"shared-entry","type":"function","parent":"client","symbols":["entry"],"shared":true}),
        json!({"id":"save-function","type":"function","parent":"server","symbols":["save"]}),
        json!({"id":"second","type":"function","parent":"server","symbols":["second"]}),
    ]);
    f.write("app", &plan);
    let v = compare(&f);
    assert_eq!(v["unplanned"].as_array().unwrap().len(), 1);
    assert_eq!(v["unplanned"][0]["count"], 2);
    plan["steps"] = json!([{"id":"call","from":"entry","to":"save-function","when":"ready"}]);
    f.write("app", &plan);
    assert_eq!(compare(&f)["state"], "built");
}

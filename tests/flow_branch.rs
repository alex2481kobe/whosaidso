mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn branch_view_uses_committed_plan_and_code_without_checkout() {
    let f = Fixture::sources(&[("app.rs", "fn main_screen() {}\n")]);
    f.write("app", &json!({"covers":{"paths":["app.rs"]},"nodes":[{"id":"screen","type":"screen","symbols":["main_screen"]}],"steps":[]}));
    commit(&f.root);
    git(&f.root, &["checkout", "-q", "-b", "feature"]);
    write(&f.root.join("app.rs"), "fn feature_screen() {}\n");
    f.write("app", &json!({"covers":{"paths":["app.rs"]},"nodes":[{"id":"feature-screen","type":"screen","symbols":["feature_screen"]}],"steps":[]}));
    commit(&f.root);
    git(&f.root, &["checkout", "-q", "main"]);
    write(&f.root.join("app.rs"), "fn dirty() {}\n");
    let v: Value = serde_json::from_str(&cli(
        &f.root,
        &["flow", "view", "app", "--branch", "feature"],
    ))
    .unwrap();
    assert_eq!(v["committed_only"], true);
    assert_eq!(v["branch"], "feature");
    assert_eq!(v["nodes"][0]["id"], "feature-screen");
    assert_eq!(v["nodes"][0]["state"], "built");
    assert_eq!(git(&f.root, &["branch", "--show-current"]).trim(), "main");
    assert_eq!(
        std::fs::read_to_string(f.root.join("app.rs")).unwrap(),
        "fn dirty() {}\n"
    );
    bad(
        &f.root,
        &["flow", "view", "app", "--branch", "absent"],
        "local branch",
    );
}

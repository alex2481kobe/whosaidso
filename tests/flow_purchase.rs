mod common;
use common::{flow::*, process::*, *};
use serde_json::Value;

#[test]
fn plans_purchase_before_code_then_progresses_and_catches_drift() {
    let f = Fixture::new();
    cli(&f.root, &["flow","put","checkout",&purchase_plan().to_string()]);
    let compare = || -> Value { serde_json::from_str(&cli(&f.root, &["flow","compare","checkout"])).unwrap() };
    let planned = compare();
    assert_eq!(planned["state"], "planned");
    assert_eq!(planned["progress"]["built"], 0);
    cli(&f.root, &["flow","drift-check"]);
    write(&f.root.join("app.rs"), "fn screen() { buy(); }\nfn buy() {}\n");
    let partial = compare();
    assert!(partial["progress"]["built"].as_u64().unwrap() > 0);
    write(&f.root.join("app.rs"), PURCHASE_CODE);
    let built = compare();
    assert_eq!(built["state"], "built", "{built}");
    assert_eq!(built["progress"]["built"], built["progress"]["total"]);
    cli(&f.root, &["flow","drift-check"]);
    assert!(cli(&f.root, &["goals"]).lines().any(|s| s.starts_with("open    g")));
    write(&f.root.join("app.rs"), PURCHASE_CODE.replace("server() { store_api(); }", "server() {}"));
    let drift = compare();
    assert_eq!(drift["steps"]["validate"]["state"], "drifted");
    assert_eq!(drift["nodes"]["purchase"]["state"], "drifted");
    bad(&f.root, &["flow","drift-check"], "drifted");
}

mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn flow_links_report_progress_without_completing_a_goal_and_refuse_unmitigated_risk() {
    let f = Fixture::new();
    write(&f.root.join("app.rs"), "fn verify() {}\n");
    let flow = json!({"goal":"g","covers":{"paths":["app.rs"]},"nodes":[
        {"id":"verify","type":"service","symbols":["verify"],"goal":"g",
         "risks":[{"id":"outage","note":"Unavailable","mitigation":{"goal":"g"}},
                  {"id":"policy","note":"Policy changes","mitigation":{"note":"store-policy"}}]}
    ],"steps":[]});
    f.run(&["flow","put","purchase",&flow.to_string()], true);
    let before = cli(&f.root, &["goals"]);
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow","compare","purchase"])).unwrap();
    assert_eq!(v["state"], "built");
    assert_eq!(v["nodes"]["verify"]["goal"], "g");
    assert_eq!(v["progress"]["built"], 1);
    assert_eq!(before, cli(&f.root, &["goals"]));
    assert!(before.lines().any(|line| line.starts_with("open    g")), "{before}");
    let mut bad = flow;
    bad["nodes"][0]["risks"][0]["mitigation"] = json!({});
    let bytes = std::fs::read(f.root.join(".whosaidso/flows/purchase.json")).unwrap();
    f.run(&["flow","put","purchase",&bad.to_string()],false);
    assert_eq!(std::fs::read(f.root.join(".whosaidso/flows/purchase.json")).unwrap(), bytes);
}

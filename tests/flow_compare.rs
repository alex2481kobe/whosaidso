mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

fn compare(f: &Fixture) -> Value {
    serde_json::from_str(&cli(&f.root, &["flow", "compare", "feature"])).unwrap()
}
#[test]
fn compares_coverage_and_rolls_up_without_judging_outside() {
    let f = Fixture::sources(&[("app.rs", "fn ready() {}\nfn duplicate() {}\nmod other { fn duplicate() {} }\n"), ("outside.rs", "fn ignored() { surprise(); }\n"), ("app.xyz", "unreadable language")]);
    f.write("feature", &json!({"covers":{"paths":["app.*"]},"nodes":[
        {"id":"feature","type":"feature"},
        {"id":"built","type":"function","parent":"feature","symbols":["ready"]},
        {"id":"planned","type":"function","parent":"feature","symbols":["future"]},
        {"id":"drifted","type":"function","parent":"feature","symbols":["ready","gone"]},
        {"id":"missing","type":"function","parent":"feature","symbols":["removed"],"status":"built"},
        {"id":"ambiguous","type":"function","parent":"feature","symbols":["duplicate"]},
        {"id":"unknown","type":"function","parent":"feature","paths":["app.xyz"]},
        {"id":"authored","type":"action","parent":"feature"}
    ],"steps":[]}));
    let v = compare(&f);
    for state in ["built", "planned", "drifted", "missing", "ambiguous", "unknown", "authored"] {
        assert_eq!(v["nodes"][state]["state"], state, "{v}");
    }
    assert_eq!(v["nodes"]["feature"]["state"], "drifted");
    assert!(!v["unplanned"].to_string().contains("outside.rs"));
    write(&f.root.join("app.rs"), "fn ready() {}\nfn duplicate() {}\nfn surprise() {}\nfn ready2() { surprise(); }\n");
    let v = compare(&f);
    assert!(v["unplanned"].as_array().unwrap().len() > 0, "{v}");
}

#[test]
fn a_planned_parent_cannot_hide_unknown_or_unplanned_work() {
    let f = Fixture::sources(&[("app.rs", "fn screen() { boundary(); }\nfn boundary() {}\n")]);
    f.write("feature", &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","symbols":["screen"]},
        {"id":"boundary","type":"service","symbols":["boundary"],"note":"Local test boundary"}
    ],"steps":[]}));
    assert_eq!(compare(&f)["state"], "unplanned");
}

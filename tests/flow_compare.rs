mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

fn compare(f: &Fixture) -> Value {
    serde_json::from_str(&cli(&f.root, &["flow", "compare", "feature"])).unwrap()
}
#[test]
fn compares_coverage_and_rolls_up_without_judging_outside() {
    let f = Fixture::sources(&[
        (
            "app.rs",
            "fn ready() {}\nfn duplicate() {}\nmod other { fn duplicate() {} }\n",
        ),
        ("outside.rs", "fn ignored() { surprise(); }\n"),
        ("app.xyz", "unreadable language"),
    ]);
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
    for state in [
        "built",
        "planned",
        "drifted",
        "missing",
        "ambiguous",
        "unknown",
        "authored",
    ] {
        assert_eq!(v["nodes"][state]["state"], state, "{v}");
    }
    assert_eq!(v["nodes"]["feature"]["state"], "drifted");
    assert!(!v["unplanned"].to_string().contains("outside.rs"));
    write(
        &f.root.join("app.rs"),
        "fn ready() {}\nfn duplicate() {}\nfn surprise() {}\nfn ready2() { surprise(); }\n",
    );
    let v = compare(&f);
    assert!(!v["unplanned"].as_array().unwrap().is_empty(), "{v}");
}

#[test]
fn a_planned_parent_cannot_hide_unknown_or_unplanned_work() {
    let f = Fixture::sources(&[("app.rs", "fn screen() { boundary(); }\nfn boundary() {}\n")]);
    f.write(
        "feature",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","symbols":["screen"]},
        {"id":"boundary","type":"service","symbols":["boundary"],"note":"Local test boundary"}
    ],"steps":[]}),
    );
    assert_eq!(compare(&f)["state"], "unplanned");
}

#[test]
fn layered_parts_do_not_invent_internal_crossings_and_high_level_steps_cover_descendants() {
    let f = Fixture::sources(&[(
        "app.rs",
        "fn screen() { helper(); }\nfn helper() { server(); }\nfn server() {}\n",
    )]);
    f.write(
        "feature",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"feature","type":"feature","paths":["app.rs"]},
        {"id":"screen","type":"screen","parent":"feature","symbols":["screen","helper"]},
        {"id":"server","type":"service","parent":"feature","symbols":["server"],"note":"Local test"}
    ],"steps":[{"id":"call","from":"screen","to":"server","when":"ready"}]}),
    );
    assert_eq!(compare(&f)["state"], "built");
    let mut flow = value(&f.root.join(".whosaidso/flows/feature.json"));
    flow["nodes"][0].as_object_mut().unwrap().remove("paths");
    flow["nodes"][1]["symbols"] = json!(["screen"]);
    flow["nodes"]
        .as_array_mut()
        .unwrap()
        .push(json!({"id":"helper","type":"function","parent":"screen","symbols":["helper"]}));
    flow["steps"]
        .as_array_mut()
        .unwrap()
        .push(json!({"id":"tap","from":"screen","to":"helper","when":"tap"}));
    f.write("feature", &flow);
    assert_eq!(compare(&f)["state"], "built");
}

#[test]
fn planned_absence_does_not_hide_unknown_coverage_in_parent_rollup() {
    let f = Fixture::sources(&[("app.xyz", "unsupported source")]);
    f.write(
        "feature",
        &json!({"covers":{"paths":["app.*"]},"nodes":[
        {"id":"feature","type":"feature"},
        {"id":"future","type":"screen","parent":"feature","symbols":["future"]}
    ],"steps":[]}),
    );
    let v = compare(&f);
    assert_eq!(v["state"], "unknown");
    assert_eq!(v["nodes"]["feature"]["state"], "unknown");
}

#[test]
fn declared_boundaries_define_the_comparison_universe_without_path_globs() {
    let f = Fixture::sources(&[(
        "app.rs",
        "fn screen() { server(); }\nfn server() {}\nfn other() { private(); }\nfn private() {}\n",
    )]);
    let mut flow = json!({"covers":{"boundaries":["server"]},"nodes":[
        {"id":"screen","type":"screen","symbols":["screen"]},
        {"id":"server","type":"service","symbols":["server"],"note":"Local test"}
    ],"steps":[]});
    f.write("feature", &flow);
    let v = compare(&f);
    assert_eq!(v["state"], "unplanned");
    assert_eq!(v["unplanned"].as_array().unwrap().len(), 1);
    flow["steps"] = json!([{"id":"call","from":"screen","to":"server","when":"ready"}]);
    f.write("feature", &flow);
    assert_eq!(compare(&f)["state"], "built");
}

#[test]
fn narrow_comparison_keeps_candidates_outside_its_paths_and_boundary_callers() {
    let f = Fixture::sources(&[
        ("app.rs", "fn screen() { server(); }\n"),
        ("api.rs", "fn server() {}\n"),
        ("other.rs", "fn server() {}\n"),
    ]);
    f.write(
        "feature",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"screen","type":"screen","paths":["app.rs"]},
        {"id":"api","type":"service","paths":["api.rs"],"note":"Local test"}
    ],"steps":[{"id":"call","from":"screen","to":"api","when":"ready"}]}),
    );
    let v = compare(&f);
    assert_eq!(v["steps"]["call"]["state"], "ambiguous");
    assert!(!v["unplanned"].as_array().unwrap().is_empty());
    f.write(
        "feature",
        &json!({"covers":{"boundaries":["api"]},"nodes":[
        {"id":"api","type":"service","paths":["api.rs"],"note":"Local test"}
    ],"steps":[]}),
    );
    let v = compare(&f);
    assert!(
        v["unplanned"]
            .as_array()
            .unwrap()
            .iter()
            .any(|e| e["path"] == "app.rs")
    );
}

#[test]
fn a_narrow_flow_can_anchor_an_opaque_symbol_id() {
    let f = Fixture::sources(&[
        ("app.rs", "fn selected() {}\n"),
        ("outside.rs", "fn unrelated() {}\n"),
    ]);
    let top: Value = serde_json::from_str(&cli(&f.root, &["flow", "map"])).unwrap();
    let file = top["nodes"]
        .as_array()
        .unwrap()
        .iter()
        .find(|n| n["path"] == "app.rs")
        .unwrap();
    let layer: Value = serde_json::from_str(&cli(
        &f.root,
        &["flow", "map", file["id"].as_str().unwrap()],
    ))
    .unwrap();
    f.write("feature",&json!({"covers":{"paths":["app.rs"]},"nodes":[{"id":"selected","type":"function","symbols":[layer["nodes"][0]["symbol"]]}],"steps":[]}));
    assert_eq!(compare(&f)["state"], "built");
}

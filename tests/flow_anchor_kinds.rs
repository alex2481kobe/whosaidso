mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn name_anchors_prefer_a_function_over_a_module_and_never_bind_locals() {
    let f = Fixture::sources(&[
        (
            "api.rs",
            "mod goals {}\nfn goals() { let goals = 1; let chosen = 2; }\n",
        ),
        ("main.rs", "fn main() { goals(); }\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["*.rs"]},"nodes":[
        {"id":"caller","type":"part","paths":["main.rs"],"symbols":["main"]},
        {"id":"api","type":"part","paths":["api.rs"]},
        {"id":"goals","parent":"api","type":"part","paths":["api.rs"],"symbols":["goals"]},
        {"id":"local-only","parent":"api","type":"part","paths":["api.rs"],"symbols":["chosen"]}
    ],"steps":[{"id":"call","from":"caller","to":"goals","when":"ready"}]}),
    );
    let syntax: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(
        syntax["nodes"]["goals"]["binding_state"], "built",
        "{syntax}"
    );
    let mut main = scip::field(1, b"main.rs");
    let mut definition = scip::field(1, &[0, 3, 7]);
    definition.extend(scip::field(2, b"scip fixture app 1 main()."));
    definition.extend([24, 1]);
    definition.extend(scip::field(7, &[0, 0, 22]));
    main.extend(scip::field(2, &definition));
    scip::import(
        &f.root,
        &[
            scip::doc(
                "api.rs",
                "scip fixture app 1 goals/",
                "goals",
                &[(&[0, 4, 9], true)],
            ),
            scip::doc(
                "api.rs",
                "scip fixture app 1 goals().",
                "goals",
                &[(&[1, 3, 8], true)],
            ),
            scip::doc("api.rs", "local 0", "goals", &[(&[1, 17, 22], true)]),
            scip::doc("api.rs", "local 1", "chosen", &[(&[1, 32, 38], true)]),
            scip::field(2, &main),
            scip::doc(
                "main.rs",
                "scip fixture app 1 goals().",
                "goals",
                &[(&[0, 12, 17], false)],
            ),
        ],
    );
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(v["nodes"]["goals"]["binding_state"], "built", "{v}");
    assert_eq!(v["nodes"]["goals"]["symbol_count"], 1);
    assert_eq!(v["nodes"]["local-only"]["binding_state"], "drifted");
    assert_eq!(v["nodes"]["local-only"]["symbol_count"], 0);
    assert_eq!(v["steps"]["call"]["state"], "built", "{v}");
    assert_eq!(v["steps"]["call"]["evidence"], "precise");
    assert!(v["unowned"].as_array().unwrap().is_empty(), "{v}");
}

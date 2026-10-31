mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn precise_imports_do_not_double_count_a_call_on_the_same_line() {
    let f = Fixture::sources(&[
        ("app.js", "import { send } from './api.js'; send();\n"),
        ("api.js", "export function send() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["*.js"]},"nodes":[
        {"id":"caller","type":"part","paths":["app.js"]},
        {"id":"api","type":"part","paths":["api.js"]}
    ],"steps":[]}),
    );
    let symbol = "scip fixture app 1 api/send().";
    assert!(
        scip::import(
            &f.root,
            &[
                scip::doc("api.js", symbol, "send", &[(&[0, 16, 20], true)]),
                scip::doc(
                    "app.js",
                    symbol,
                    "send",
                    &[(&[0, 9, 13], false), (&[0, 33, 37], false)]
                ),
            ]
        )
        .contains("0 stale documents")
    );
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(v["unplanned"][0]["count"], 1, "{v}");
    assert_eq!(v["unplanned"][0]["evidence"], "precise");
}

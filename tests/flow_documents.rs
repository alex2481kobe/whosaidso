mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn html_documents_do_not_block_flow_checks() {
    let f = Fixture::sources(&[
        ("index.html", "<script>unread();</script>"),
        ("help.htm", "<p>Help</p>"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["*.html","*.htm"]},"nodes":[
        {"id":"page","type":"document","paths":["index.html","help.htm"]}
    ],"steps":[]}),
    );
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(v["state"], "built", "{v}");
    assert!(v["unknown"].as_array().unwrap().is_empty());
    cli(&f.root, &["flow", "drift-check"]);
}

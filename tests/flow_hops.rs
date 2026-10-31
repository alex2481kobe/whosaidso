mod common;
use common::{flow::FlowFixture, process::cli, *};
use serde_json::{Value, json};

#[test]
fn literal_hop_is_labelled_and_drifts_when_removed() {
    let f = Fixture::sources(&[("app.rs", "fn buy() { request(\"/verify\"); }\nfn verify() {}\n")]);
    f.write("purchase", &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"buy","type":"action","symbols":["buy"]},
        {"id":"verify","type":"service","symbols":["verify"],"note":"Local test boundary"}
    ],"steps":[{"id":"request","from":"buy","to":"verify","when":"receipt","literal":"/verify"}]}));
    let get = || -> Value { serde_json::from_str(&cli(&f.root, &["flow","compare","purchase"])).unwrap() };
    assert_eq!(get()["steps"]["request"]["state"], "built");
    assert_eq!(get()["steps"]["request"]["evidence"], "literal");
    write(&f.root.join("app.rs"), "fn buy() { request(\"/other\"); }\nfn verify() {}\n");
    let drift = get();
    assert_eq!(drift["steps"]["request"]["state"], "drifted");
    assert_eq!(drift["steps"]["request"]["evidence"], "literal");
    assert_eq!(drift["state"], "drifted");
}

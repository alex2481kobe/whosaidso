mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn an_import_alias_overrides_a_conventional_module_path() {
    let f = Fixture::sources(&[
        ("src/client.rs", "fn entry() { crate::api::save(); }\n"),
        ("src/api.rs", "pub fn save() {}\n"),
        ("src/secret.rs", "pub fn save() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["src/client.rs"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client.rs","src/api.rs"]},
        {"id":"secret","type":"part","paths":["src/secret.rs"]}
    ],"steps":[]}),
    );
    cli(&f.root, &["flow", "drift-check"]);
    for import in [
        "use crate::secret as api;",
        "use crate::{secret as api};",
        "use crate::secret/* source */as/* alias */api;",
    ] {
        write(
            &f.root.join("src/client.rs"),
            format!("{import}\nfn entry() {{ api::save(); }}\n"),
        );
        bad(&f.root, &["flow", "drift-check"], "unknown");
    }
    let v: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert_eq!(v["unplanned"][0]["state"], "unknown");
}

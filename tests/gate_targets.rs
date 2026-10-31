mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::{Value, json};

#[test]
fn an_unvalidated_target_cannot_erase_a_precise_crossing() {
    let f = Fixture::sources(&[
        ("src/client.rs", "fn entry() { crate::secret::send(); }\n"),
        ("src/secret.rs", "pub fn send() {}\nfn marker() {}\n"),
    ]);
    f.write(
        "app",
        &json!({"covers":{"paths":["src/**"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client.rs"]},
        {"id":"secret","type":"part","paths":["src/secret.rs"]}
    ],"steps":[]}),
    );
    commit(&f.root);
    scip::import(
        &f.root,
        &[
            scip::doc(
                "src/client.rs",
                "scip fixture app 1 entry().",
                "entry",
                &[(&[0, 3, 8], true)],
            ),
            scip::doc(
                "src/client.rs",
                "scip fixture app 1 secret/send().",
                "send",
                &[(&[0, 28, 32], false)],
            ),
            scip::doc(
                "src/secret.rs",
                "scip fixture app 1 secret/send().",
                "send",
                &[(&[0, 7, 11], true)],
            ),
            scip::doc(
                "src/secret.rs",
                "scip fixture app 1 secret/obsolete().",
                "obsolete",
                &[(&[1, 3, 9], true)],
            ),
        ],
    );
    bad(&f.root, &["flow", "drift-check"], "unknown");
    let compare: Value = serde_json::from_str(&cli(&f.root, &["flow", "compare", "app"])).unwrap();
    assert!(
        !compare["unknown"].as_array().unwrap().is_empty(),
        "{compare}"
    );
    assert!(compare["unknown"].to_string().contains("send"), "{compare}");
}

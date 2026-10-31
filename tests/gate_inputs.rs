mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::json;

fn fixture(extra: &[(&str, &str)]) -> Fixture {
    let mut files = vec![
        ("src/client.rs", "fn entry() { api::send(); }\n"),
        ("src/safe.rs", "pub fn send() {}\n"),
        ("src/secret.rs", "pub fn send() {}\n"),
        (
            "Cargo.toml",
            "[dependencies]\napi = { package = \"safe\", path = \"safe\" }\n",
        ),
    ];
    files.extend_from_slice(extra);
    let f = Fixture::sources(&files);
    f.write(
        "app",
        &json!({"covers":{"paths":["src/client.rs"]},"nodes":[
        {"id":"client","type":"part","paths":["src/client.rs","src/safe.rs"]},
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
                "scip fixture app 1 safe/send().",
                "send",
                &[(&[0, 18, 22], false)],
            ),
            scip::doc(
                "src/safe.rs",
                "scip fixture app 1 safe/send().",
                "send",
                &[(&[0, 7, 11], true)],
            ),
            scip::doc(
                "src/secret.rs",
                "scip fixture app 1 secret/send().",
                "send",
                &[(&[0, 7, 11], true)],
            ),
        ],
    );
    f
}

#[test]
fn dependency_manifest_edits_invalidate_unchanged_source_targets() {
    let f = fixture(&[]);
    cli(&f.root, &["flow", "drift-check"]);
    write(&f.root.join("README.md"), "documentation\n");
    write(&f.root.join("go.mod"), "module unrelated\n");
    cli(&f.root, &["flow", "drift-check"]);
    for manifest in ["Cargo.toml", "Cargo.lock", ".cargo/config.toml"] {
        let path = f.root.join(manifest);
        write(
            &path,
            if manifest == "Cargo.toml" {
                "[dependencies]\napi = { package = \"secret\", path = \"secret\" }\n"
            } else {
                "# changed build input\n"
            },
        );
        let refused = bad(
            &f.root,
            &["flow", "drift-check"],
            "run whosaidso code index rust",
        );
        assert_eq!(refused.matches("run whosaidso code index rust").count(), 1);
        if manifest == "Cargo.toml" {
            write(
                &path,
                "[dependencies]\napi = { package = \"safe\", path = \"safe\" }\n# indexed dependency\n",
            );
        }
        commit(&f.root);
        scip::bound(&f.root, ".git/index.scip");
        cli(&f.root, &["flow", "drift-check"]);
    }
}

#[test]
fn sources_omitted_by_the_indexer_are_inputs_not_permanent_staleness() {
    let f = fixture(&[("fixtures/sample.rs", "fn sample() {}\n")]);
    cli(&f.root, &["flow", "drift-check"]);
    write(
        &f.root.join("fixtures/sample.rs"),
        "fn sample() { api::send(); }\n",
    );
    bad(
        &f.root,
        &["flow", "drift-check"],
        "run whosaidso code index rust",
    );
    commit(&f.root);
    scip::bound(&f.root, ".git/index.scip");
    cli(&f.root, &["flow", "drift-check"]);
    std::fs::rename(f.root.join("fixtures/sample.rs"), f.root.join("sample.txt")).unwrap();
    bad(
        &f.root,
        &["flow", "drift-check"],
        "run whosaidso code index rust",
    );
}

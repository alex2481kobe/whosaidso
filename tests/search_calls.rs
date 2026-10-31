mod common;
use common::process::*;
use common::*;

#[test]
fn source_evidence_survives_data_floods_and_generated_code() {
    let f = Fixture::empty();
    for i in 0..205 {
        write(
            &f.root.join(format!("ordinary/{i}.json")),
            "{\"payment invoice reconciliation\":true}\n",
        );
    }
    write(
        &f.root.join("generated.rs"),
        "// Code generated automatically; DO NOT EDIT.\n/// Payment invoice reconciliation.\nfn generated_invoice() {}\n",
    );
    write(
        &f.root.join("manual.rs"),
        "/// Invoice reconciliation.\nfn reconcile_invoice() {}\n",
    );
    let out = cli(
        &f.root,
        &["find", "payment invoice reconciliation", "--limit", "5"],
    );
    assert!(
        out.lines().next().unwrap().contains("reconcile_invoice"),
        "{out}"
    );
    assert!(cli(&f.root, &["find", "ordinary/0.json"]).contains("ordinary/0.json"));
}

#[test]
fn partial_lexical_evidence_does_not_bury_the_semantic_head() {
    let Some(model) = std::env::var_os("WHOSAIDSO_TEST_MODEL") else {
        eprintln!("SKIP semantic head: set WHOSAIDSO_TEST_MODEL to pinned potion-base-8M");
        return;
    };
    let model = std::fs::canonicalize(model).unwrap();
    let f = Fixture::empty();
    write(
        &f.root.join("zzzz.rs"),
        "/// Bills the customer for a recurring service.\nfn charge_subscription() {}\n",
    );
    for i in 0..8 {
        write(
            &f.root.join(format!("aaaa{i}.rs")),
            format!(
                "/// Membership payment {}\nfn telescope_{i}() {{}}\n",
                "asteroid telescope galaxy cosmic planet orbit ".repeat(80)
            ),
        );
    }
    let out = success(
        command(
            &f.root,
            BIN,
            &["find", "periodic membership payment", "--limit", "5"],
        )
        .env("WHOSAIDSO_MODEL", model)
        .output()
        .unwrap(),
    );
    assert!(
        out.lines().next().unwrap().contains("charge_subscription"),
        "{out}"
    );
    assert!(out.lines().next().unwrap().contains("[embedding]"), "{out}");
}

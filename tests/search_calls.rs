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

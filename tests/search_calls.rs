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
fn find_shows_source_signatures_with_utf8_and_answer_byte_limits() {
    let f = Fixture::empty();
    for i in 0..30 {
        write(
            &f.root.join(format!("file{i}.rs")),
            format!(
                "/// Comment that is not the signature.\nfn selected({}: i32) {{ let PRIVATE_SENTINEL = 42; }}\n",
                "雪".repeat(100)
            ),
        );
    }
    let out = cli(&f.root, &["find", "selected", "--limit", "100"]);
    assert!(
        out.lines().any(|line| line.starts_with("  fn selected(")),
        "{out}"
    );
    assert!(!out.contains("PRIVATE_SENTINEL"), "{out}");
    assert!(
        out.len() <= 4096 && out.contains("output budget"),
        "{}: {out}",
        out.len()
    );
    assert!(out.lines().all(|line| line.len() <= 384), "{out}");
}

#[test]
fn grep_diagnoses_regex_forms_instead_of_claiming_absence() {
    let f = Fixture::empty();
    write(
        &f.root.join("words.txt"),
        "cat\nconcatenate\nleft|right\n\\b\n",
    );
    commit(&f.root);
    bad(&f.root, &["grep", r"\bcat\b"], "literal mode");
    bad(
        &f.root,
        &["grep", "--regex", r"left\|right"],
        "extended regex",
    );
    bad(
        &f.root,
        &["grep", "--regex", r"\d", "--in", "absent/"],
        "unsupported",
    );
    bad(&f.root, &["grep", "--regex", r"\bcat\b"], "unsupported");
    bad(&f.root, &["grep", "--regex", "[", "--in", "absent/"], "[");
    let pattern = "(^|[^[:alnum:]_])cat([^[:alnum:]_]|$)";
    let out = command(&f.root, BIN, &["grep", "--regex", pattern])
        .output()
        .unwrap();
    assert!(String::from_utf8_lossy(&out.stderr).contains("Git extended regex"));
    assert_eq!(success(out), git(&f.root, &["grep", "-n", "-E", pattern]));
    assert_eq!(
        cli(&f.root, &["grep", "--literal", r"\b"]),
        "words.txt:4:\\b\n"
    );
}

#[test]
fn ambiguous_show_includes_bodies_with_a_shared_small_budget() {
    let f = Fixture::empty();
    for (name, marker) in [
        ("a", "first_only"),
        ("b", "second_only"),
        ("c", "third_only"),
        ("d", "fourth_only"),
    ] {
        write(
            &f.root.join(format!("{name}.rs")),
            format!("fn duplicate() {{ let {marker} = 42; }}\n"),
        );
    }
    let out = cli(&f.root, &["code", "show", "duplicate"]);
    for marker in [
        "first_only",
        "second_only",
        "third_only",
        "path:line:name",
        "shown 3 of 4",
    ] {
        assert!(out.contains(marker), "{out}");
    }
    assert!(!out.contains("fourth_only"), "{out}");
    write(
        &f.root.join("a.rs"),
        format!(
            "fn duplicate() {{\n{}\n}}\n",
            format!("let s = \"{}\";\n", "雪".repeat(1000)).repeat(120)
        ),
    );
    let out = cli(&f.root, &["code", "show", "duplicate"]);
    assert!(
        out.len() < 20_000 && out.contains("body truncated"),
        "{} bytes",
        out.len()
    );
}

#[test]
fn show_accepts_printed_locations_and_rejects_invalid_selectors() {
    let f = Fixture::empty();
    write(
        &f.root.join("lib.rs"),
        "// leading comment\nfn selected() { let answer = 42; }\n",
    );
    let printed = cli(&f.root, &["find", "selected"]);
    for selector in [
        "lib.rs:selected",
        "lib.rs:2:selected",
        "lib.rs::selected",
        printed.lines().next().unwrap(),
    ] {
        let body = cli(&f.root, &["code", "show", selector]);
        assert!(body.contains("let answer = 42"), "{selector}: {body}");
    }
    for selector in [
        "lib.rs:0:selected",
        "lib.rs:x:selected",
        "lib.rs:2:",
        "lib.rs:",
    ] {
        bad(&f.root, &["code", "show", selector], "invalid selector");
    }
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

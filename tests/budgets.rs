mod common;
use common::process::*;
use common::{records as r, *};

#[test]
fn excess_records_name_the_oldest_topics_to_retire() {
    let f = Fixture::new();
    let root = &f.root;
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"limits":{"notes":1}}"#,
    );
    for id in ["old", "new"] {
        write(
            &root.join(format!(".whosaidso/notes/{id}.md")),
            format!(
                "---\n{{\"type\":\"idea\",\"status\":\"resolved\",\"title\":\"{id}\",\"goal\":\"g\"}}\n---\nTopic.\n"
            ),
        );
        commit(root);
    }
    let out = bad(root, &["check"], "limits.notes");
    assert!(out.contains("retire old"));
    let p = root.join(".whosaidso/notes/old.md");
    cli(
        root,
        &["note", "retire", "old", &hash(&std::fs::read(p).unwrap())],
    );
    cli(root, &["check"]);
}

#[test]
fn oversized_tests_name_the_largest_file_and_off_disables_the_budget() {
    let f = Fixture::new();
    let root = &f.root;
    write(&root.join("src/main.rs"), "fn main() {}\n");
    write(&root.join("spec/example.rs"), "x".repeat(200));
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"test_paths":["spec/**"],"limits":{"tests":1}}"#,
    );
    assert!(bad(root, &["check"], "limits.tests").contains("spec/example.rs"));
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"test_paths":["spec/**"],"limits":{"tests":"off"}}"#,
    );
    cli(root, &["check"]);
}

#[test]
fn slow_checks_are_stopped_recorded_and_named_and_off_allows_them() {
    let f = Fixture::new();
    let root = &f.root;
    r::check_command(root, &["sh", "-c", "sleep 0.2"]);
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"limits":{"check_seconds":0.05}}"#,
    );
    commit(root);
    bad(root, &["prove", "g"], "limits.check_seconds");
    assert_eq!(paths(&root.join(".whosaidso/proofs")).len(), 1);
    assert!(bad(root, &["check"], "limits.check_seconds").contains("g"));
    write(
        &root.join(".whosaidso/config.json"),
        r#"{"limits":{"check_seconds":"off"}}"#,
    );
    commit(root);
    cli(root, &["prove", "g", "--reason", "timing demonstration"]);
    cli(root, &["check"]);
}

#[test]
fn ci_refuses_source_unit_tests_and_a_library_target() {
    let f = Fixture::empty();
    let root = &f.root;
    let ci = include_str!("../.github/workflows/ci.yml");
    let script = ci
        .split("name: End-to-end test policy\n")
        .nth(1)
        .expect("CI policy step")
        .split("run: |\n")
        .nth(1)
        .unwrap()
        .lines()
        .take_while(|l| l.starts_with("          "))
        .map(|l| &l[10..])
        .collect::<Vec<_>>()
        .join("\n");
    write(&root.join("src/main.rs"), "fn main() {}\n");
    success(command(root, "sh", &["-ec", &script]).output().unwrap());
    for text in ["#[test]\nfn unit() {}\n", "#[cfg(test)]\nmod unit {}\n"] {
        write(&root.join("src/main.rs"), text);
        assert!(
            !command(root, "sh", &["-ec", &script])
                .output()
                .unwrap()
                .status
                .success()
        );
    }
    write(&root.join("src/main.rs"), "fn main() {}\n");
    write(&root.join("src/lib.rs"), "");
    assert!(
        !command(root, "sh", &["-ec", &script])
            .output()
            .unwrap()
            .status
            .success()
    );
}

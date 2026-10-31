mod common;
use common::{
    BIN, Fixture,
    process::{cli, command, failure},
    scip::{doc, field, import},
    write,
};

#[test]
fn bodies_and_outlines_preserve_ranges_and_report_ambiguity() {
    let source = format!(
        "// {}\nfn target() {{\n    target();\n}}\nfn unrelated() {{}}\n",
        "padding ".repeat(100)
    );
    let fixture = Fixture::sources(&[
        ("one.rs", &source),
        ("two.rs", "fn duplicate() {}\n"),
        ("three.rs", "fn duplicate() {}\n"),
    ]);
    let root = &fixture.root;
    let body = cli(root, &["code", "show", "target"]);
    assert!(body.contains("one.rs:2:0-4:1"), "{body}");
    assert!(body.contains("fn target() {\n    target();\n}"), "{body}");
    assert!(
        !body.contains("unrelated") && !body.contains("padding"),
        "{body}"
    );
    assert!(body.len() < source.len());
    let outline = cli(root, &["code", "outline", "one.rs"]);
    assert!(
        outline.contains("target") && outline.contains("unrelated"),
        "{outline}"
    );
    assert!(
        !outline.contains("target();") && outline.len() < source.len(),
        "{outline}"
    );
    let ambiguous = cli(root, &["code", "show", "duplicate"]);
    assert!(
        ambiguous.contains("ambiguous: 2") && !ambiguous.contains("fn duplicate()"),
        "{ambiguous}"
    );
    assert!(cli(root, &["code", "show", "two.rs::duplicate"]).contains("fn duplicate() {}"));
    write(&root.join("unsupported.rb"), "def target; end\n");
    assert!(cli(root, &["code", "outline", "unsupported.rb"]).contains("not indexed"));
    import(
        root,
        &[doc(
            "one.rs",
            "scip-test pkg project 1 target().",
            "target",
            &[(&[1, 3, 9], true)],
        )],
    );
    let precise = cli(root, &["code", "show", "target"]);
    assert!(
        precise.contains("[precise]") && precise.contains("body unavailable"),
        "{precise}"
    );
}

#[test]
fn indexer_is_pinned_direct_and_failure_preserves_live_facts() {
    use common::{executable, process::success};
    let fixture = Fixture::sources(&[("main.go", "package example\nfunc target() {}\n")]);
    let root = &fixture.root;
    let bin = root.join(".git/bin");
    std::fs::create_dir_all(&bin).unwrap();
    let mut bytes = field(1, &[]);
    bytes.extend(doc(
        "main.go",
        "scip-go gomod example v0.0.0 target().",
        "target",
        &[(&[1, 5, 11], true)],
    ));
    write(&root.join(".git/input.scip"), bytes);
    executable(
        &bin.join("scip-go"),
        r##"#!/usr/bin/python3
import sys, pathlib
root=pathlib.Path.cwd()
if '--version' in sys.argv:
 print('scip-go '+('9.9.9' if (root/'.git/wrong').exists() else '0.2.7'));sys.exit()
a=sys.argv[1:]
assert a[0]=='index' and '--module-root' in a and '--module-version=v0.0.0' in a
out=pathlib.Path(a[a.index('--output')+1])
assert not out.exists()
out.write_bytes((root/'.git/input.scip').read_bytes())
if (root/'.git/fail').exists(): sys.exit(3)
if (root/'.git/bad').exists(): out.write_bytes(b'bad')
if (root/'.git/edit').exists(): (root/'main.go').write_text('package example\nfunc changed() {}\n')
"##,
    );
    let path = std::env::join_paths([
        bin.clone(),
        std::path::PathBuf::from("/usr/bin"),
        std::path::PathBuf::from("/bin"),
    ])
    .unwrap();
    let run = || {
        command(root, BIN, &["code", "index", "go"])
            .env("PATH", &path)
            .output()
            .unwrap()
    };
    assert!(success(run()).contains("scip:"));
    assert!(cli(root, &["code", "defs", "target"]).contains("[precise]"));
    for (flag, message) in [
        ("fail", "indexer failed"),
        ("bad", "SCIP"),
        ("wrong", "requires"),
        ("edit", "changed during indexing"),
    ] {
        write(&root.join(format!(".git/{flag}")), "");
        failure(run(), message);
        std::fs::remove_file(root.join(format!(".git/{flag}"))).unwrap();
        if flag != "edit" {
            assert!(cli(root, &["code", "defs", "target"]).contains("[precise]"));
        }
    }
}

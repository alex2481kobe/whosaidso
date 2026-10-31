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

#[test]
fn impact_walks_chains_diamonds_cycles_and_honors_depth_and_pages() {
    let fixture = Fixture::sources(&[
        (
            "graph.rs",
            "fn top() { left(); right(); }\nfn left() { leaf(); }\nfn right() { leaf(); }\nfn leaf() { top(); }\n",
        ),
        ("same.rs", "fn duplicate() {}\n"),
        ("other.rs", "fn duplicate() {}\n"),
    ]);
    let root = &fixture.root;
    let answer = cli(root, &["code", "callees", "top", "--depth", "2"]);
    for name in ["left", "right", "leaf"] {
        assert!(answer.contains(&format!(" {name} [syntactic]")), "{answer}");
    }
    assert_eq!(answer.matches(" leaf [syntactic]").count(), 1, "{answer}");
    assert!(
        answer.contains("depth limit") && answer.contains("coverage:"),
        "{answer}"
    );
    let shallow = cli(root, &["code", "callees", "top", "--depth", "1"]);
    assert!(!shallow.contains(" leaf [syntactic]"), "{shallow}");
    let reverse = cli(root, &["code", "callers", "leaf", "--depth", "3"]);
    assert!(
        reverse.contains(" top [syntactic]") && reverse.contains("rows=3"),
        "{reverse}"
    );
    let path = cli(root, &["code", "affected", "graph.rs", "--depth", "3"]);
    assert!(path.contains("seeds=4"), "{path}");
    let ambiguous = cli(root, &["code", "callers", "duplicate"]);
    assert!(
        ambiguous.contains("ambiguous: 2")
            && ambiguous.contains("same.rs")
            && ambiguous.contains("other.rs"),
        "{ambiguous}"
    );
    let page = cli(
        root,
        &[
            "code", "callees", "top", "--depth", "3", "--limit", "1", "--offset", "1",
        ],
    );
    assert!(
        page.contains("shown 1 of 3") && page.contains("offset=1"),
        "{page}"
    );
    let token = page
        .lines()
        .next()
        .unwrap()
        .split_whitespace()
        .find_map(|s| s.strip_prefix("snapshot="))
        .unwrap();
    let stable = cli(
        root,
        &[
            "code",
            "callees",
            "top",
            "--depth",
            "3",
            "--offset",
            "2",
            "--snapshot",
            token,
        ],
    );
    assert!(stable.contains("offset=2"));
    write(&root.join("graph.rs"), "fn changed() {}\n");
    failure(
        command(
            root,
            BIN,
            &["code", "callees", "changed", "--snapshot", token],
        )
        .output()
        .unwrap(),
        "snapshot expired",
    );
}

#[test]
fn graph_combines_fresh_precise_and_syntax_and_reports_implementations() {
    let fixture = Fixture::sources(&[
        ("api.rs", "fn api() {}\nfn implementation() { api(); }\n"),
        ("use.rs", "fn user() { api(); }\n"),
    ]);
    let root = &fixture.root;
    let api = "scip-test pkg project 1 api().";
    let implementation = "scip-test pkg project 1 implementation().";
    let mut document = field(1, b"api.rs");
    for (symbol, range, enclosing, def) in [
        (api, &[0, 3, 6][..], &[0, 0, 11][..], true),
        (implementation, &[1, 3, 17][..], &[1, 0, 30][..], true),
        (api, &[1, 22, 25][..], &[1, 22, 27][..], false),
    ] {
        let mut occ = field(1, range);
        occ.extend(field(2, symbol.as_bytes()));
        occ.extend([24, u8::from(def)]);
        occ.extend(field(7, enclosing));
        document.extend(field(2, &occ));
    }
    let mut info = field(1, implementation.as_bytes());
    let mut relationship = field(1, api.as_bytes());
    relationship.extend([24, 1]);
    info.extend(field(4, &relationship));
    document.extend(field(3, &info));
    import(root, &[field(2, &document)]);
    let answer = cli(root, &["code", "callers", "api", "--implementations"]);
    assert!(
        answer.contains("implementation [precise]") && answer.contains("user [syntactic]"),
        "{answer}"
    );
    assert!(
        answer.contains("implementations: 1 recorded") && answer.contains("precise=1 syntactic=1"),
        "{answer}"
    );
    assert_eq!(
        answer.matches("implementation [precise]").count(),
        1,
        "{answer}"
    );
    write(
        &root.join("api.rs"),
        "fn api() {}\nfn replacement() { api(); }\n",
    );
    let stale = cli(root, &["code", "callers", "api"]);
    assert!(
        stale.contains("replacement [syntactic]") && stale.contains("precise documents are stale"),
        "{stale}"
    );
}

#[path = "v032_impact/review.rs"]
mod review;

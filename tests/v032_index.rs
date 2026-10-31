mod common;
use common::{
    BIN, Fixture,
    process::{cli, command, git},
    scip::field,
};
use std::{
    fs,
    path::Path,
    process::Stdio,
    time::{Duration, Instant},
};

fn database(root: &Path) -> rusqlite::Connection {
    let common = git(
        root,
        &["rev-parse", "--path-format=absolute", "--git-common-dir"],
    );
    let path = Path::new(common.trim()).join("whosaidso/code/facts.db");
    assert!(path.is_file(), "shared fact store missing");
    rusqlite::Connection::open_with_flags(path, rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY).unwrap()
}
fn scalar(db: &rusqlite::Connection, sql: &str) -> i64 {
    db.query_row(sql, [], |r| r.get(0)).unwrap()
}

#[test]
fn worktrees_share_facts_without_sharing_files() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    let mut trees = Vec::new();
    for i in 0..4 {
        let path = root.join(format!("tree{i}"));
        git(
            &root,
            &["worktree", "add", "-q", "--detach", path.to_str().unwrap()],
        );
        fs::write(
            path.join("own.rs"),
            format!("fn own_{i}() {{ shared(); }}\n"),
        )
        .unwrap();
        trees.push(path);
    }
    std::thread::scope(|s| {
        for (i, path) in trees.iter().enumerate() {
            s.spawn(move || {
                assert!(cli(path, &["code", "defs", &format!("own_{i}")]).contains("own.rs:1"));
                assert!(
                    cli(path, &["code", "defs", &format!("own_{}", (i + 1) % 4)])
                        .contains("none recorded")
                );
                assert!(cli(path, &["code", "refs", "shared"]).contains("own.rs:1"));
            });
        }
    });
    let db = database(&root);
    assert_eq!(scalar(&db, "SELECT count(*) FROM facts"), 5);
    assert_eq!(scalar(&db, "SELECT count(*) FROM worktrees"), 4);
    assert_eq!(scalar(&db, "SELECT count(*) FROM generations"), 4);
    assert_eq!(
        scalar(
            &db,
            "SELECT count(*) FROM generations g WHERE EXISTS (SELECT 1 FROM manifest_files m JOIN facts f ON f.id=m.fact WHERE m.manifest=g.manifest AND instr(g.stamp,substr(f.extractor,1,instr(f.extractor,':')))=1)"
        ),
        4
    );
}

#[test]
fn killed_writer_keeps_last_published_generation() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    cli(&root, &["code", "defs", "shared"]);
    let before = scalar(&database(&root), "SELECT max(id) FROM generations");
    fs::write(root.join("shared.rs"), "fn replacement() {}\n").unwrap();
    let ready = root.join("writer-ready");
    let mut child = command(&root, BIN, &["code", "defs", "replacement"])
        .env("WHOSAIDSO_INDEX_PAUSE", &ready)
        .stdout(Stdio::null())
        .spawn()
        .unwrap();
    let start = Instant::now();
    while !ready.exists() && start.elapsed() < Duration::from_secs(3) {
        std::thread::sleep(Duration::from_millis(10));
    }
    let reached = ready.exists();
    child.kill().unwrap();
    child.wait().unwrap();
    assert!(reached, "writer never reached the publication fault point");
    let db = database(&root);
    assert_eq!(scalar(&db, "SELECT max(id) FROM generations"), before);
    assert_eq!(
        scalar(&db, "SELECT count(*) FROM syntax_defs WHERE name='shared'"),
        1
    );
    assert_eq!(
        scalar(
            &db,
            "SELECT count(*) FROM syntax_defs WHERE name='replacement'"
        ),
        0
    );
    assert!(cli(&root, &["code", "defs", "replacement"]).contains("shared.rs:1"));
}

#[test]
fn disk_edits_staging_deletes_and_renames_remain_visible() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    cli(&root, &["code", "defs", "shared"]);
    fs::write(root.join("shared.rs"), "fn staged() {}\n").unwrap();
    git(&root, &["add", "shared.rs"]);
    assert!(cli(&root, &["code", "defs", "staged"]).contains("shared.rs:1"));
    fs::write(root.join("shared.rs"), "fn edited() {}\n").unwrap();
    assert!(cli(&root, &["code", "defs", "edited"]).contains("shared.rs:1"));
    assert!(cli(&root, &["code", "defs", "staged"]).contains("none recorded"));
    fs::rename(root.join("shared.rs"), root.join("renamed.rs")).unwrap();
    assert!(cli(&root, &["code", "defs", "edited"]).contains("renamed.rs:1"));
    fs::remove_file(root.join("renamed.rs")).unwrap();
    assert!(cli(&root, &["code", "defs", "edited"]).contains("none recorded"));
    assert_eq!(
        scalar(&database(&root), "SELECT count(*) FROM manifest_files"),
        0
    );
}

#[test]
fn recreated_worktree_gets_a_new_identity() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    let path = root.join("tree");
    for _ in 0..2 {
        git(
            &root,
            &["worktree", "add", "-q", "--detach", path.to_str().unwrap()],
        );
        cli(&path, &["code", "defs", "shared"]);
        fs::write(path.join("orphan.rs"), "fn orphan() {}\n").unwrap();
        cli(&path, &["code", "defs", "orphan"]);
        fs::remove_file(path.join("orphan.rs")).unwrap();
        git(&root, &["worktree", "remove", path.to_str().unwrap()]);
    }
    assert_eq!(
        scalar(&database(&root), "SELECT count(*) FROM worktrees"),
        2
    );
    cli(&root, &["code", "defs", "shared"]);
    assert_eq!(scalar(&database(&root), "SELECT count(*) FROM facts"), 1);
}

#[test]
fn fallback_sees_edits_even_when_git_assumes_unchanged() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    git(&root, &["config", "core.fsmonitor", "false"]);
    cli(&root, &["code", "defs", "shared"]);
    git(&root, &["update-index", "--assume-unchanged", "shared.rs"]);
    fs::write(root.join("shared.rs"), "fn edited() {}\n").unwrap();
    assert!(cli(&root, &["code", "defs", "edited"]).contains("shared.rs:1"));
}

#[test]
fn precise_import_retains_containers_relationships_and_docs() {
    let fixture = Fixture::sources(&[("shared.rs", "fn shared() {}\n")]);
    let root = fixture.root.clone();
    fs::write(
        root.join("calls.rs"),
        "fn target() {}\nfn outer() {\n    target();\n}\n",
    )
    .unwrap();
    let target = "scip-test pkg project 1 target().";
    let outer = "scip-test pkg project 1 outer().";
    let mut doc = field(1, b"calls.rs");
    for (symbol, range, def, enclosing) in [
        (target, &[0, 3, 9][..], true, &[0, 0, 14][..]),
        (outer, &[1, 3, 8][..], true, &[1, 0, 3, 1][..]),
        (target, &[2, 4, 10][..], false, &[2, 4, 13][..]),
    ] {
        let mut occ = field(1, range);
        occ.extend(field(2, symbol.as_bytes()));
        occ.extend([24, u8::from(def)]);
        occ.extend(field(
            7,
            if symbol == outer {
                &[0, 0, 0]
            } else {
                enclosing
            },
        ));
        if symbol == outer {
            occ.extend(field(11, &[8, 1, 24, 3, 32, 1]));
        }
        if !def {
            occ.extend(field(4, b"Specialized call docs"));
        }
        doc.extend(field(2, &occ));
    }
    let mut info = field(1, target.as_bytes());
    info.extend(field(3, b"Target docs"));
    let mut relationship = field(1, b"scip-test pkg project 1 api().");
    relationship.extend([16, 1, 24, 1, 32, 1, 40, 1]);
    info.extend(field(4, &relationship));
    doc.extend(field(3, &info));
    let mut index = field(1, &[]);
    index.extend(field(2, &doc));
    fs::write(root.join("index.scip"), &index).unwrap();
    assert!(cli(&root, &["code", "import-scip", "index.scip"]).contains("0 stale documents"));
    let answer = cli(&root, &["code", "refs", "target"]);
    assert!(
        answer.contains("calls.rs:3  [precise] in outer"),
        "{answer}"
    );
    let gd = git(&root, &["rev-parse", "--absolute-git-dir"]);
    let db = rusqlite::Connection::open(Path::new(gd.trim()).join("whosaidso/cache/precise.db"))
        .unwrap();
    assert_eq!(
        scalar(
            &db,
            "SELECT count(*) FROM scip_docs_text WHERE text IN ('Target docs','Specialized call docs')"
        ),
        2
    );
    assert_eq!(
        scalar(
            &db,
            "SELECT count(*) FROM scip_relationships WHERE is_reference AND is_implementation AND is_type_definition AND is_definition"
        ),
        1
    );
    let tree = root.join("other");
    git(
        &root,
        &["worktree", "add", "-q", "--detach", tree.to_str().unwrap()],
    );
    fs::write(
        tree.join("calls.rs"),
        fs::read(root.join("calls.rs")).unwrap(),
    )
    .unwrap();
    assert!(!cli(&tree, &["code", "refs", "target"]).contains("[precise]"));
    let typed = field(11, &[8, 1, 24, 3, 32, 1]);
    let at = index.windows(typed.len()).position(|w| w == typed).unwrap();
    index[at + 5] = 40;
    fs::write(root.join("index.scip"), &index).unwrap();
    assert!(cli(&root, &["code", "import-scip", "index.scip"]).contains("1 stale documents"));
    assert!(!cli(&root, &["code", "refs", "target"]).contains("[precise]"));
}

#[path = "v032_index/languages.rs"]
mod languages;

#[test]
fn branch_snapshot_survives_another_worktrees_head_query() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let other = fixture.worktree("other");
    fs::write(other.join("one.rs"), "fn different() {}\n").unwrap();
    common::commit(&other);
    let first = cli(root, &["review", "--base", "HEAD"]);
    let before = first
        .lines()
        .next()
        .unwrap()
        .split_whitespace()
        .find_map(|s| s.strip_prefix("before="))
        .unwrap();
    cli(&other, &["review", "--base", "HEAD"]);
    let result = command(
        root,
        BIN,
        &["review", "--base", "HEAD", "--before-snapshot", before],
    )
    .output()
    .unwrap();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
}

#[test]
fn oversized_reread_cannot_poison_a_smaller_files_facts() {
    let fixture = Fixture::sources(&[("tracked.rs", "fn tracked() {}\n")]);
    let root = &fixture.root;
    let small = "fn restored() {}\n";
    fs::write(root.join("growing.rs"), small).unwrap();
    fs::write(root.join(".git/grow"), "").unwrap();
    let bin = root.join(".git/bin");
    let real = std::env::split_paths(common::process::tool_path())
        .map(|p| p.join("git"))
        .find(|p| p.is_file())
        .unwrap();
    common::executable(
        &bin.join("git"),
        r##"#!/usr/bin/python3
import os, pathlib, sys
root = pathlib.Path.cwd()
if sys.argv[1:2] == ['cat-file'] and (root/'.git/grow').exists():
    with (root/'growing.rs').open('ab') as f: f.truncate(64*1024*1024+1)
    (root/'.git/grow').unlink()
os.execv(os.environ['REAL_GIT'], [os.environ['REAL_GIT'], *sys.argv[1:]])
"##,
    );
    let path = std::env::join_paths(
        std::iter::once(bin).chain(std::env::split_paths(common::process::tool_path())),
    )
    .unwrap();
    let out = command(root, BIN, &["code", "defs", "restored"])
        .env("PATH", path)
        .env("REAL_GIT", real)
        .output()
        .unwrap();
    common::process::success(out);
    assert!(!root.join(".git/grow").exists(), "growth race did not run");
    fs::write(root.join("growing.rs"), small).unwrap();
    let answer = cli(root, &["code", "defs", "restored"]);
    assert!(answer.contains("growing.rs:1"), "{answer}");
}

#[test]
fn changed_header_invalidates_precise_rows_coverage_and_relationships() {
    let fixture = Fixture::sources(&[
        ("api.h", "inline void target() {}\n"),
        (
            "caller.cpp",
            "#include \"api.h\"\nvoid caller() { target(); }\n",
        ),
    ]);
    let root = &fixture.root;
    let target = "scip-test pkg project 1 target().";
    let caller = "scip-test pkg project 1 caller().";
    let mut document = field(1, b"caller.cpp");
    let mut info = field(1, caller.as_bytes());
    let mut relation = field(1, target.as_bytes());
    relation.extend([24, 1]);
    info.extend(field(4, &relation));
    document.extend(field(3, &info));
    common::scip::import(
        root,
        &[
            common::scip::doc("api.h", target, "target", &[(&[0, 12, 18], true)]),
            common::scip::doc("caller.cpp", caller, "caller", &[(&[1, 5, 11], true)]),
            common::scip::doc("caller.cpp", target, "target", &[(&[1, 16, 22], false)]),
            field(2, &document),
        ],
    );
    assert!(cli(root, &["code", "refs", "target"]).contains("[precise]"));
    assert!(
        cli(root, &["code", "callers", "target", "--implementations"])
            .contains("implementations: 1 recorded")
    );
    fs::write(root.join("api.h"), "inline void replacement() {}\n").unwrap();
    let answer = cli(root, &["code", "refs", "target"]);
    assert!(
        answer.contains("caller.cpp:2")
            && answer.contains("[syntactic]")
            && answer.contains("precise documents are stale")
            && !answer.contains("[precise]"),
        "{answer}"
    );
    let coverage = cli(root, &["code", "callers", "caller", "--implementations"]);
    assert!(
        coverage.contains("precise=0") && coverage.contains("implementations: 0 recorded"),
        "{coverage}"
    );
}

#[test]
fn changed_build_environment_invalidates_the_precise_run() {
    let fixture = Fixture::sources(&[("one.rs", "fn target() {}\n")]);
    let root = &fixture.root;
    common::scip::import(
        root,
        &[common::scip::doc(
            "one.rs",
            "scip-test pkg project 1 target().",
            "target",
            &[(&[0, 3, 9], true)],
        )],
    );
    assert!(cli(root, &["code", "defs", "target"]).contains("[precise]"));
    let answer = common::process::success(
        command(root, BIN, &["code", "defs", "target"])
            .env("RUSTFLAGS", "--cfg changed")
            .output()
            .unwrap(),
    );
    assert!(
        answer.contains("[syntactic]")
            && answer.contains("precise documents are stale")
            && !answer.contains("[precise]"),
        "{answer}"
    );
}

#[test]
fn unbound_old_artifact_cannot_hide_a_new_syntax_definition() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let mut artifact = field(1, &[]);
    artifact.extend(common::scip::doc(
        "one.rs",
        "scip-test pkg project 1 original().",
        "original",
        &[(&[0, 3, 11], true)],
    ));
    fs::write(root.join(".git/old.scip"), artifact).unwrap();
    fs::write(root.join("one.rs"), "fn original() {}\nfn added() {}\n").unwrap();
    cli(root, &["code", "import-scip", ".git/old.scip"]);
    for (name, location) in [("added", "one.rs:2"), ("original", "one.rs:1")] {
        let answer = cli(root, &["code", "defs", name]);
        assert!(
            answer.contains(location)
                && answer.contains("[syntactic]")
                && answer.contains("unknown freshness")
                && !answer.contains("[precise]"),
            "{answer}"
        );
    }
}

#[test]
fn old_branch_snapshots_are_collected_and_their_tokens_expire() {
    let fixture = Fixture::sources(&[("one.rs", "fn original() {}\n")]);
    let root = &fixture.root;
    let commit = common::head(root);
    let first = cli(root, &["review"]);
    let token = first
        .lines()
        .next()
        .unwrap()
        .split_whitespace()
        .find_map(|s| s.strip_prefix("before="))
        .unwrap();
    for i in 0..10 {
        fs::write(root.join("one.rs"), format!("fn changed_{i}() {{}}\n")).unwrap();
        common::commit(root);
        cli(root, &["review"]);
    }
    assert!(
        scalar(
            &database(root),
            "SELECT count(*) FROM generations WHERE worktree IS NULL"
        ) <= 8,
        "committed snapshots grew beyond retention"
    );
    common::process::failure(
        command(
            root,
            BIN,
            &["review", "--base", &commit, "--before-snapshot", token],
        )
        .output()
        .unwrap(),
        "snapshot expired",
    );
    assert!(cli(root, &["code", "defs", "changed_9"]).contains("one.rs:1"));
}

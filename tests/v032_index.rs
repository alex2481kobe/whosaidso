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
    assert!(common::scip::bound(&root, "index.scip").contains("0 stale documents"));
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
    assert!(common::scip::bound(&root, "index.scip").contains("1 stale documents"));
    assert!(!cli(&root, &["code", "refs", "target"]).contains("[precise]"));
}

#[path = "v032_index/languages.rs"]
mod languages;

#[path = "v032_index/regressions.rs"]
mod regressions;

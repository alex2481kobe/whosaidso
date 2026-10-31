mod common;
use common::{Fixture, process::cli, write};
use rusqlite::Connection;
use std::{fs, time::Instant};

#[test]
fn forgotten_revisions_do_not_accumulate_payloads_or_vocabulary() {
    let f = Fixture::sources(&[("one.rs", "fn keeper() {}\n")]);
    let other = f.worktree("other");
    write(&other.join("other.rs"), "fn siblingneedle() {}\n");
    cli(&other, &["find", "siblingneedle"]);
    let db = Connection::open(f.root.join(".git/whosaidso/search/search.db")).unwrap();
    let mut early = 0;
    for revision in 0..12 {
        let body: String = (0..500)
            .map(|n| format!("discarded{revision:02}word{n:04} "))
            .collect();
        write(&f.root.join("churn.md"), body);
        cli(&f.root, &["find", "keeper"]);
        fs::remove_file(f.root.join("churn.md")).unwrap();
        cli(&f.root, &["find", "keeper"]);
        let bytes: i64 = db.query_row("SELECT coalesce(sum(length(address)+length(title)+length(body)+length(detail)),0) FROM payloads WHERE id NOT IN (SELECT payload FROM members)", [], |r| r.get(0)).unwrap();
        assert!(
            bytes <= 65536,
            "revision {revision}: {bytes} retained orphan payload bytes"
        );
        let templates: i64 = db.query_row("SELECT count(*) FROM blob_templates WHERE address NOT IN (SELECT stamp FROM stamps WHERE source LIKE 'file:%')", [], |r| r.get(0)).unwrap();
        assert_eq!(templates, 0, "orphan templates");
        for table in ["all_vocab", "trigram_terms", "search_trigrams"] {
            let count: i64 = db
                .query_row(
                    &format!(
                        "SELECT count(*) FROM {table} WHERE {} LIKE '%discarded%'",
                        "term"
                    ),
                    [],
                    |r| r.get(0),
                )
                .unwrap();
            assert_eq!(count, 0, "orphan terms in {table}");
        }
        let live: i64 = db.query_row("SELECT (page_count-freelist_count)*page_size FROM pragma_page_count(),pragma_freelist_count(),pragma_page_size()", [], |r| r.get(0)).unwrap();
        if revision == 1 {
            early = live;
        }
        if revision > 1 {
            assert!(
                live <= early + 65536,
                "live storage grew: {early} -> {live}"
            );
        }
    }
    assert!(cli(&other, &["find", "siblingneedle"]).contains("symbol other.rs:1"));
}

#[test]
#[ignore = "paired warm query timing"]
fn warm_retrieval_timing() {
    let f = Fixture::empty();
    for n in 0..150 {
        write(
            &f.root.join(format!("file{n}.rs")),
            format!("fn needle_{n}() {{}}\n"),
        );
    }
    cli(&f.root, &["find", "needle"]);
    let mut micros = Vec::new();
    for _ in 0..31 {
        let start = Instant::now();
        assert!(cli(&f.root, &["find", "needle"]).contains("symbol"));
        micros.push(start.elapsed().as_micros());
    }
    micros.sort();
    println!(
        "warm find: median={}us p90={}us samples={} files=150",
        micros[15],
        micros[27],
        micros.len()
    );
}

mod common;
use common::process::*;
use common::{records as r, *};
use serde_json::json;
use std::{fs, time::Instant};
fn stress(count: usize, mut seed: u64, collect: bool) {
    let start = Instant::now();
    let f = Fixture::empty();
    let tested = f.root.parent().unwrap().join("tested");
    write(&tested, "");
    let mut ids: Vec<_> = (0..count).map(|n| format!("g{n:03}")).collect();
    for i in (1..ids.len()).rev() {
        seed ^= seed << 13;
        seed ^= seed >> 7;
        seed ^= seed << 17;
        ids.swap(i, (seed as usize) % (i + 1));
    }
    let mut batches = vec![Vec::new(); 6];
    for (n, id) in ids.iter().enumerate() {
        batches[n % 6].push(id.clone());
    }
    for batch in &batches {
        let check = "set -e; files=$(git ls-files '*.txt'); test -n \"$files\"; for file in $files; do test \"$(cat \"$file\")\" = \"$file\"; done; git rev-parse HEAD >> \"$TESTED\"";
        for id in batch {
            let p = r::goal(&f.root, id);
            let mut v = value(&p);
            v["check"] = json!(["sh", "-c", check]);
            r::put(&f.root, "goals", id, &v);
        }
    }
    write(
        &f.root.join(".whosaidso/config.json"),
        json!({"focus":count,"limits":{"active":count}}).to_string(),
    );
    cli(&f.root, &["install-hooks"]);
    commit(&f.root);
    let remote = f.remote();
    let tips = f.root.parent().unwrap().join("tips");
    executable(
        &remote.join("hooks/update"),
        format!(
            "#!/bin/sh\n[ \"$1\" = refs/heads/main ] || exit 0\ncandidate=$(git show -s --format='%(trailers:key=WhoSaidSo-Receipt,valueonly)' \"$3\" | sed -n 's/.*\"candidate\":\"\\([a-f0-9]*\\)\".*/\\1/p')\n[ -n \"$candidate\" ] && grep -qx \"$candidate\" '{}' || {{ echo 'untested tip' >&2; exit 1; }}\n",
            tested.display()
        ),
    );
    executable(
        &remote.join("hooks/post-receive"),
        format!(
            "#!/bin/sh\nwhile read -r old tip reference; do [ \"$reference\" != refs/heads/main ] || echo \"$tip\" >> '{}'; done\n",
            tips.display()
        ),
    );
    let clones = [f.clone("one"), f.clone("two")];
    let mut workers = Vec::new();
    for (n, clone) in clones.iter().enumerate() {
        cli(clone, &["install-hooks"]);
        for i in 0..3 {
            let name = format!("worker-{n}-{i}");
            let path = f.root.parent().unwrap().join(&name);
            git(
                clone,
                &[
                    "worktree",
                    "add",
                    "-q",
                    "-b",
                    &name,
                    path.to_str().unwrap(),
                    "main",
                ],
            );
            workers.push((name, path));
        }
    }
    // The publication instrument must first reject a tip it has never observed being tested.
    work(&workers[0].1, "unproven", "none");
    failure(
        command(
            &workers[0].1,
            "git",
            &["push", "--no-verify", "origin", "HEAD:main"],
        )
        .output()
        .unwrap(),
        "untested tip",
    );
    eprintln!("stress setup {:.3}s", start.elapsed().as_secs_f64());
    for ((_, path), batch) in workers.iter().zip(&batches) {
        cli(path, &["start", &batch[0]]);
    }
    bad(&workers[1].1, &["start", &batches[0][0]], "claimed");
    std::thread::scope(|s| {
        let handles:Vec<_>=workers.iter().zip(&batches).map(|((_,path),batch)| {
            let tested=&tested;
            s.spawn(move || {
                for id in batch {
                    write(&path.join(format!(".whosaidso/notes/{id}.md")),format!("---\n{{\"type\":\"idea\",\"status\":\"resolved\",\"title\":\"Measured work\",\"goal\":\"{id}\"}}\n---\n{id} result\n"));
                    work(path,&format!("{id}.txt"),id);
                }
                commit(path);
                if !collect { success(command(path,BIN,&["land","--reason","shared output integrity check"]).env("TESTED",tested).output().unwrap()); }
            })
        }).collect();
        for h in handles {
            h.join().unwrap();
        }
    });
    if collect {
        for first in [0, 3] {
            for other in first + 1..first + 3 {
                cli(
                    &workers[other].1,
                    &[
                        "assign",
                        &batches[other][0],
                        &format!("branch:{}", workers[first].0),
                    ],
                );
                git(
                    &workers[first].1,
                    &["merge", "--no-edit", &workers[other].0],
                );
            }
        }
        std::thread::scope(|s| {
            let handles: Vec<_> = [0, 3]
                .into_iter()
                .map(|i| {
                    let path = &workers[i].1;
                    let tested = &tested;
                    s.spawn(move || {
                        success(
                            command(
                                path,
                                BIN,
                                &["land", "--reason", "shared output integrity check"],
                            )
                            .env("TESTED", tested)
                            .output()
                            .unwrap(),
                        )
                    })
                })
                .collect();
            for h in handles {
                h.join().unwrap();
            }
        });
        for i in [1, 2, 4, 5] {
            cli(&workers[i].1, &["start", "none"]);
        }
    }
    eprintln!("stress landed {:.3}s", start.elapsed().as_secs_f64());
    git(&f.root, &["fetch", "origin"]);
    git(&f.root, &["merge", "--ff-only", "origin/main"]);
    let goals = cli(&f.root, &["goals"]);
    assert!(
        goals.starts_with(&format!("{count}/{count} done")),
        "{goals}"
    );
    for id in &ids {
        assert!(f.root.join(format!("{id}.txt")).exists());
        let bundle =
            fs::read_to_string(f.root.join(format!(".whosaidso/done/{id}.jsonl"))).unwrap();
        assert!(bundle.contains(&format!("{id} result")));
    }
    assert!(git(&remote, &["for-each-ref", "refs/whosaidso/claims"]).is_empty());
    let published = fs::read_to_string(tips).unwrap();
    assert_eq!(published.lines().count(), if collect { 2 } else { 6 });
    for tip in published.lines() {
        assert!(git(&f.root, &["show", "-s", "--format=%B", tip]).contains("WhoSaidSo-Receipt:"));
    }
    eprintln!(
        "stress: {count} goals, 2 clones, 6 worktrees, {:.3}s",
        start.elapsed().as_secs_f64()
    );
}
#[test]
fn fixed_seed_many_workers_preserve_every_record_and_test_every_tip() {
    stress(20, 0x5eed, true);
}
#[test]
#[ignore = "manual randomized landing stress"]
fn many_workers_manual() {
    stress(200, 0xc0ffee, false);
}

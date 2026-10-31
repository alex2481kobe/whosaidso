mod common;
use common::process::*;
use common::*;
use std::sync::{
    Barrier,
    atomic::{AtomicBool, Ordering},
};

#[test]
#[ignore = "manual: concurrent cold publication under CPU load"]
fn mixed_worktree_readers_and_first_publishers_wait_for_each_other() {
    let rounds =
        std::env::var("WHOSAIDSO_STRESS_ROUNDS").map_or(20, |s| s.parse::<usize>().unwrap());
    let stop = AtomicBool::new(false);
    std::thread::scope(|scope| {
        for _ in 0..4 {
            scope.spawn(|| {
                let mut n = 1_u64;
                while !stop.load(Ordering::Relaxed) {
                    n = std::hint::black_box(n.wrapping_mul(6364136223846793005).wrapping_add(1));
                }
            });
        }
        let result = std::panic::catch_unwind(|| {
            for round in 0..rounds {
                let f = Fixture::sources(&[("shared.rs", "fn shared() { return; }\n")]);
                let trees: Vec<_> = (0..12)
                    .map(|i| f.worktree(&format!("worker-{i}")))
                    .collect();
                let start = Barrier::new(trees.len() * 3);
                std::thread::scope(|workers| {
                    for (i, tree) in trees.iter().enumerate() {
                        write(
                            &tree.join("own.rs"),
                            format!("fn own_{i}() {{ shared(); }}\n"),
                        );
                        for args in [
                            ["code", "defs", "shared"].as_slice(),
                            &["find", "shared"],
                            &["grep", "shared"],
                        ] {
                            let start = &start;
                            workers.spawn(move || {
                                start.wait();
                                let out = command(tree, BIN, args).output().unwrap();
                                assert!(
                                    out.status.success(),
                                    "round {round}, worker {i}, {args:?}: {out:?}"
                                );
                                assert!(
                                    String::from_utf8_lossy(&out.stdout).contains("shared.rs"),
                                    "{out:?}"
                                );
                            });
                        }
                    }
                });
                eprintln!("completed round {round}");
            }
        });
        stop.store(true, Ordering::Relaxed);
        result.unwrap();
    });
}

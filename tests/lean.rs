// Serialized pruning and caps through the real binary in one isolated Git project.
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
    time::{SystemTime, UNIX_EPOCH},
};

fn run(root: &Path, program: &str, args: &[&str]) -> Output {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", root)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env_remove("WHOSAIDSO_BLOBS");
    if program == "git" {
        c.args([
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.com",
            "-c",
            "init.defaultBranch=main",
        ]);
    }
    c.args(args).output().unwrap()
}
fn ok(root: &Path, program: &str, args: &[&str]) -> String {
    let out = run(root, program, args);
    assert!(
        out.status.success(),
        "{args:?}: {} {}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, args: &[&str]) -> String {
    ok(root, "git", args)
}
fn cli(root: &Path, args: &[&str]) -> String {
    ok(root, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn bad(root: &Path, expected: &str) {
    let out = run(root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]);
    assert_eq!(out.status.code(), Some(1), "expected {expected}: {out:?}");
    assert!(
        String::from_utf8_lossy(&out.stdout).contains(expected),
        "expected {expected}: {out:?}"
    );
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn addressed(dir: &Path, value: &serde_json::Value) -> PathBuf {
    let bytes = serde_json::to_vec(value).unwrap();
    let sha: String = Sha256::digest(&bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    let path = dir.join(format!("{sha}.json"));
    write(&path, bytes);
    path
}
fn files(dir: &Path) -> Vec<PathBuf> {
    let mut paths: Vec<_> = fs::read_dir(dir)
        .unwrap()
        .map(|e| e.unwrap().path())
        .collect();
    paths.sort();
    paths
}
fn size(dir: &Path) -> (usize, u64) {
    files(dir)
        .into_iter()
        .map(|p| {
            if p.is_dir() {
                size(&p)
            } else {
                (1, fs::metadata(p).unwrap().len())
            }
        })
        .fold((0, 0), |(n, b), (m, c)| (n + m, b + c))
}
fn goal(dir: &Path, id: &str) {
    write(
        &dir.join(format!("goals/{id}.json")),
        serde_json::json!({"title":id,"done_when":"done","check":"test","broader":"p"}).to_string(),
    );
}
fn commit(root: &Path, message: &str) {
    git(root, &["add", ".whosaidso"]);
    git(root, &["commit", "--allow-empty", "-m", message]);
}

#[test]
fn ledger_stays_small_and_checks_its_caps() {
    let root = std::env::temp_dir().join(format!(
        "lean-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir_all(&root).unwrap();
    git(&root, &["init"]);
    let records = root.join(".whosaidso");
    write(&records.join(".gitignore"), "cache/\n");
    write(
        &records.join("phases/p.json"),
        br#"{"title":"Phase","order":1}"#,
    );
    let instrument = records.join("instruments/test.json");
    let tool = serde_json::json!({"run":["sh","-c","sh -c 'read n < .whosaidso/cache/serial; if [ \"$n\" -ge 100 ]; then head -c 20000 /dev/zero | tr \"\\000\" x; printf END; else printf run; fi; exit $(((n + n / 60) % 2))'"],"answers":"works"});
    write(&instrument, tool.to_string());
    for i in 0..20 {
        goal(&records, &format!("g{i}"));
    }
    commit(&root, "fixture");
    let initial = git(&root, &["rev-parse", "HEAD"]).trim().to_owned();
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    let proofs = records.join("proofs");
    let seed = serde_json::json!({"goal":"g0","instrument":"test","commit":initial,"exit_code":0,"output_sha256":"0".repeat(64),"output_tail":"","ran_at":0});
    let seed_path = addressed(&proofs, &seed);
    commit(&root, "shared old proof");
    git(&root, &["branch", "b"]);
    git(&root, &["checkout", "-b", "a"]);
    for branch in 0..2 {
        if branch == 1 {
            git(&root, &["checkout", "b"]);
        }
        for n in 0..60 {
            if branch == 1 && n == 40 {
                commit(&root, "records before joining branches");
                git(&root, &["merge", "--no-edit", "a"]);
                std::thread::sleep(std::time::Duration::from_secs(1));
            }
            let i = branch * 60 + n;
            write(&records.join("cache/serial"), format!("{i}\n"));
            git(
                &root,
                &["commit", "--allow-empty", "-m", &format!("run {i}")],
            );
            let id = format!("g{}", n % 20);
            let answer = cli(&root, &["prove", &id]);
            assert!(
                answer.contains(if (i + branch) % 2 == 0 {
                    " pass "
                } else {
                    " fail "
                }),
                "{answer}"
            );
            if n % 2 == 0 {
                assert!(
                    cli(
                        &root,
                        &[
                            "ask",
                            &format!("g{}", (n / 2) % 5),
                            &format!("request {i:03}")
                        ]
                    )
                    .starts_with("ask:")
                );
            }
        }
        assert!(
            seed_path.exists(),
            "prove must retain the shared ancestor proof"
        );
        commit(&root, "branch records");
    }
    git(&root, &["checkout", "main"]);
    git(&root, &["merge", "--no-edit", "a"]);
    git(&root, &["merge", "--no-edit", "b"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    assert_eq!(files(&proofs).len(), 121, "prove never deletes records");
    let asks = records.join("asks");
    assert_eq!(files(&asks).len(), 60, "ask never deletes records");
    let mut expected = BTreeMap::new();
    for path in files(&asks) {
        let ask: serde_json::Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        expected
            .entry(ask["goal"].as_str().unwrap().to_owned())
            .or_insert_with(Vec::new)
            .push((ask["ran_at"].as_i64().unwrap(), path));
    }
    let mut newest = Vec::new();
    for group in expected.values_mut() {
        group.sort_by(|a, b| b.cmp(a));
        newest.extend(group.iter().take(5).map(|(_, p)| p.clone()));
    }
    newest.sort();
    let warnings = cli(&root, &["check"]);
    assert!(
        warnings.contains("superseded proofs; run whosaidso prune")
            && warnings.contains("asks cap 5: count 12; run whosaidso prune"),
        "{warnings}"
    );
    git(&root, &["branch", "before-prune"]);
    let before = cli(&root, &["goals"]);
    assert!(
        before.contains("done") && before.contains("failing"),
        "{before}"
    );
    let pruned = cli(&root, &["prune"]);
    commit(&root, "prune with landing");
    assert_eq!(
        cli(&root, &["goals"]),
        before,
        "prune must preserve identical goals"
    );
    assert_eq!(files(&asks), newest, "keep five asks by ran_at then sha");
    assert_eq!(
        files(&proofs).len(),
        20,
        "one selected proof per goal after the branches join"
    );
    assert_eq!(pruned, "prune: 101 proofs, 35 asks, 0 media archived\n");
    let checked = cli(&root, &["check"]);
    assert!(
        checked.starts_with("check: ok") && !checked.contains("prune"),
        "{checked}"
    );
    let (count, bytes) = size(&records);
    println!("lean: {count} files, {bytes} bytes after 120 proves and 60 asks");
    assert!(count <= 20 + 20 + 25 + 4, "bounded file count: {count}");
    assert!(bytes < 1024 * 1024, "ledger bytes {bytes}");
    assert_eq!(
        cli(&root, &["prune"]),
        "prune: 0 proofs, 0 asks, 0 media archived\n"
    );
    for path in files(&proofs) {
        let proof: serde_json::Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        let tail = proof["output_tail"].as_str().unwrap();
        assert_eq!(tail.len(), 16 * 1024);
        assert!(tail.ends_with("END"));
        let mut full = vec![b'x'; 20000];
        full.extend_from_slice(b"END");
        let hash: String = Sha256::digest(&full)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        assert_eq!(proof["output_sha256"], hash);
    }
    git(&root, &["checkout", "before-prune"]);
    git(
        &root,
        &["commit", "--allow-empty", "-m", "ongoing branch work"],
    );
    assert!(cli(&root, &["prove", "g0"]).contains(" pass "));
    assert!(cli(&root, &["ask", "g0", "ongoing request"]).starts_with("ask:"));
    commit(&root, "ongoing branch records");
    git(&root, &["merge", "--no-edit", "main"]);
    assert!(!git(&root, &["status", "--porcelain"]).contains("UU"));
    git(&root, &["checkout", "main"]);

    // Unlanded proofs and rejected legacy records cannot replace the proof that goals selects.
    let kept: serde_json::Value = files(&proofs)
        .iter()
        .map(|p| serde_json::from_slice(&fs::read(p).unwrap()).unwrap())
        .find(|p: &serde_json::Value| p["goal"] == "g0")
        .unwrap();
    let mut extra = kept.clone();
    extra["result"] = "unknown".into();
    extra["ran_at"] = i64::MAX.into();
    let legacy = addressed(&proofs, &extra);
    assert!(cli(&root, &["goals"]).contains("done"));
    fs::remove_file(legacy).unwrap();
    let mut covering = seed.clone();
    covering["instrument"] = "other".into();
    write(
        &records.join("instruments/other.json"),
        br#"{"run":["true"],"answers":"other"}"#,
    );
    commit(&root, "covering instrument");
    covering["commit"] = git(&root, &["rev-parse", "HEAD"]).trim().into();
    let covered = addressed(&proofs, &covering);
    git(&root, &["checkout", "-b", "unlanded"]);
    git(&root, &["commit", "--allow-empty", "-m", "unlanded"]);
    extra["commit"] = git(&root, &["rev-parse", "HEAD"]).trim().into();
    extra.as_object_mut().unwrap().remove("result");
    extra["exit_code"] = 0.into();
    let unlanded = addressed(&proofs, &extra);
    git(&root, &["checkout", "main"]);
    commit(&root, "evidence before collection");
    let before = cli(&root, &["goals"]);
    cli(&root, &["prune"]);
    assert_eq!(
        cli(&root, &["goals"]),
        before,
        "unlanded proof must not displace landed state"
    );
    assert!(
        unlanded.exists() && !covered.exists(),
        "keep divergent proofs and retire unused instruments"
    );
    git(&root, &["checkout", "main"]);

    // A landed side branch need not be in the selected proof's ancestry.
    commit(&root, "records before side branch");
    git(&root, &["checkout", "-b", "side", &initial]);
    let mut side_paths = Vec::new();
    for n in 1..=3 {
        git(
            &root,
            &["commit", "--allow-empty", "-m", &format!("side work {n}")],
        );
        let mut side = seed.clone();
        side["commit"] = git(&root, &["rev-parse", "HEAD"]).trim().into();
        side["ran_at"] = n.into();
        if n == 2 {
            side["output_tail"] = "other evidence".into();
        }
        if n == 3 {
            side["exit_code"] = 1.into();
        }
        assert!(
            !run(
                &root,
                "git",
                &[
                    "merge-base",
                    "--is-ancestor",
                    side["commit"].as_str().unwrap(),
                    kept["commit"].as_str().unwrap()
                ]
            )
            .status
            .success()
        );
        side_paths.push(addressed(&proofs, &side));
    }
    commit(&root, "side proofs");
    git(&root, &["checkout", "main"]);
    git(&root, &["merge", "--no-edit", "side"]);
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    commit(&root, "evidence before collection");
    let before = cli(&root, &["goals"]);
    cli(&root, &["prune"]);
    assert_eq!(cli(&root, &["goals"]), before);
    assert!(
        side_paths.iter().all(|p| !p.exists()),
        "prune every superseded landed side proof"
    );

    // Every state other than done counts, including failing, stale and unproved goals.
    let states = cli(&root, &["goals"]);
    let done = states
        .lines()
        .filter(|l| l.trim_start().starts_with("done "))
        .count();
    for i in 20..100 + done {
        goal(&records, &format!("g{i}"));
    }
    assert!(cli(&root, &["check"]).contains("check: ok"));
    goal(&records, "excess");
    bad(&root, "open goals cap 100: count 101");
    fs::remove_file(records.join("goals/excess.json")).unwrap();
    let done_id = states
        .lines()
        .find(|l| l.trim_start().starts_with("done "))
        .unwrap()
        .split_whitespace()
        .nth(1)
        .unwrap();
    write(&root.join("later-work"), "work");
    git(&root, &["add", "later-work"]);
    git(
        &root,
        &[
            "commit",
            "--allow-empty",
            "-m",
            "later",
            "-m",
            &format!("Goal: {done_id}"),
        ],
    );
    git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]);
    bad(&root, "open goals cap 100: count 101");
    for i in 20..100 + done {
        fs::remove_file(records.join(format!("goals/g{i}.json"))).unwrap();
    }

    let phase = records.join("phases/p.json");
    let original = fs::read(&phase).unwrap();
    let mut padded = original.clone();
    padded.resize(64 * 1024, b' ');
    write(&phase, &padded);
    assert!(cli(&root, &["check"]).contains("check: ok"));
    padded.push(b' ');
    write(&phase, padded);
    bad(&root, "record file cap 65536 bytes: count 65537");
    write(&phase, original);
    let image = records.join("media/oversized.json");
    write(&image, vec![b' '; 65537]);
    bad(&root, "record file cap 65536 bytes: count 65537");
    fs::remove_file(image).unwrap();
    extra = kept;
    extra["output_tail"] = "x".repeat(16 * 1024 + 1).into();
    let oversized = addressed(&proofs, &extra);
    bad(&root, "output tail cap 16384 bytes: count 16385");
    fs::remove_file(oversized).unwrap();
    write(&records.join("cache/invalid-output"), vec![0xff; 20000]);
    write(
        &instrument,
        br#"{"run":["cat",".whosaidso/cache/invalid-output"],"answers":"works"}"#,
    );
    commit(&root, "updated output instrument");
    assert!(cli(&root, &["prove", "g1"]).contains(" pass "));
    let invalid: serde_json::Value = files(&proofs)
        .iter()
        .map(|p| serde_json::from_slice(&fs::read(p).unwrap()).unwrap())
        .find(|p: &serde_json::Value| p["output_tail"].as_str().unwrap().ends_with('\u{fffd}'))
        .unwrap();
    assert!(invalid["output_tail"].as_str().unwrap().len() <= 16 * 1024);
    assert!(cli(&root, &["check"]).contains("check: ok"));
    fs::remove_dir_all(root).unwrap();
}

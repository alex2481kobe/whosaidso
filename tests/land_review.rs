mod common;
use common::process::*;
use common::{records as r, *};

#[test]
fn a_fresh_candidate_failure_blocks_a_future_dated_pass() {
    let f = Fixture::new();
    r::check_command(&f.root, &["sh", "-c", "test ! -f broken"]);
    commit(&f.root);
    work(&f.root, "feature", "g");
    f.remote();
    let lane = f.worktree("worker");
    cli(&lane, &["install-hooks"]);
    commit(&lane);
    cli(&lane, &["start", "g"]);
    r::proof(&f.root, &head(&f.root), i64::MAX - 1, 0);
    commit(&f.root);
    git(&f.root, &["push", "origin", "main"]);
    work(&lane, "broken", "none");
    let before = git(&f.root, &["ls-remote", "origin", "refs/heads/main"]);
    bad(
        &lane,
        &["land", "--reason", "existing behavior"],
        "candidate check failed",
    );
    assert_eq!(
        before,
        git(&f.root, &["ls-remote", "origin", "refs/heads/main"])
    );
}

#[test]
fn absolute_check_arguments_use_each_restored_revision() {
    let f = Fixture::new();
    let lane = f.worktree("worker");
    let check = lane.join("check.sh");
    write(
        &check,
        "test -f \"$1\"\nprintf '%s\\n' \"$1\" \"$2\" \"$3\"\ntest -f \"$1\"\n",
    );
    r::check_command(
        &lane,
        &[
            "sh",
            check.to_str().unwrap(),
            lane.join("feature").to_str().unwrap(),
            &format!("--input={}/feature", lane.display()),
            &format!("--linked={}/alias", lane.display()),
        ],
    );
    std::os::unix::fs::symlink("feature", lane.join("alias")).unwrap();
    work(&lane, "feature", "g");
    commit(&lane);
    cli(&lane, &["prove", "g", "--reason", "existing behavior"]);
    let proof = value(&paths(&lane.join(".whosaidso/proofs"))[0]);
    assert_ne!(proof["main"]["exit_code"], 0);
    assert_eq!(proof["exit_code"], 0);
    let output = proof["output_tail"].as_str().unwrap();
    assert!(!output.contains(lane.to_str().unwrap()), "{output}");
    assert!(output.contains("/prove/worktree/alias"), "{output}");
    assert!(output.contains("/prove/worktree/feature"), "{output}");
    assert!(output.contains("--input="), "{output}");
}

#[test]
fn queued_work_reruns_checks_for_an_already_done_goal() {
    let f = Fixture::new();
    r::goal(&f.root, "parent");
    let mut goal = value(&f.root.join(".whosaidso/goals/g.json"));
    goal["parent"] = serde_json::json!("parent");
    goal["check"] = serde_json::json!(["sh", "-c", "test ! -f broken"]);
    r::put(&f.root, "goals", "g", &goal);
    commit(&f.root);
    work(&f.root, "feature", "g");
    r::proof(&f.root, &head(&f.root), 1, 0);
    commit(&f.root);
    let base = head(&f.root);
    work(&f.root, "update", "g");
    cli(&f.root, &["prove", "g", "--reason", "existing behavior"]);
    commit(&f.root);
    work(&f.root, "broken", "none");
    bad(
        &f.root,
        &["gate", "--ci", &format!("{base}..HEAD")],
        "queued check failed for g",
    );
}

#[test]
fn resuming_a_receipt_publishes_media_to_the_current_remote() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    attach(&lane, "landing measurement");
    commit(&lane);
    failure(
        command(&lane, BIN, &["land", "--reason", "shared check"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != before-push ]",
            )
            .output()
            .unwrap(),
        "before-push",
    );
    let replacement = f.root.parent().unwrap().join("replacement.git");
    git(
        &f.root,
        &["init", "--bare", "-q", replacement.to_str().unwrap()],
    );
    git(&f.root, &["push", replacement.to_str().unwrap(), "main"]);
    git(
        &lane,
        &["remote", "set-url", "origin", replacement.to_str().unwrap()],
    );
    cli(&lane, &["land", "--reason", "shared check"]);
    assert!(!git(&lane, &["ls-remote", "origin", "refs/whosaidso/blobs"]).is_empty());
    let other = f.root.parent().unwrap().join("reader");
    git(
        &f.root,
        &[
            "clone",
            "-q",
            replacement.to_str().unwrap(),
            other.to_str().unwrap(),
        ],
    );
    cli(&other, &["sync"]);
    let check = cli(&other, &["check"]);
    assert!(!check.contains("missing"), "{check}");
    assert!(
        other
            .join(format!(
                ".git/whosaidso/blobs/{}.png",
                hash(&png("landing measurement"))
            ))
            .is_file()
    );
}

#[test]
fn offline_claims_must_acquire_remote_ownership_before_landing() {
    let f = Fixture::new();
    cli(&f.root, &["install-hooks"]);
    commit(&f.root);
    let remote = f.remote();
    let first = f.clone("first");
    let second = f.clone("second");
    let mut notices = Vec::new();
    for (root, agent) in [(&first, "first"), (&second, "holder")] {
        cli(root, &["install-hooks"]);
        git(root, &["remote", "set-url", "origin", "unreachable"]);
        notices.push(success(
            command(root, BIN, &["start", "g"])
                .env("WHOSAIDSO_AGENT", agent)
                .output()
                .unwrap(),
        ));
        work(root, "feature", "g");
        git(
            root,
            &["remote", "set-url", "origin", remote.to_str().unwrap()],
        );
    }
    success(
        command(&second, BIN, &["start", "g"])
            .env("WHOSAIDSO_AGENT", "holder")
            .output()
            .unwrap(),
    );
    let tip = git(&remote, &["rev-parse", "main"]);
    failure(
        command(&first, BIN, &["land", "--reason", "shared check"])
            .env("WHOSAIDSO_AGENT", "first")
            .output()
            .unwrap(),
        "holder",
    );
    assert!(
        notices.iter().all(|text| text.contains("local only")),
        "{notices:?}"
    );
    assert_eq!(tip, git(&remote, &["rev-parse", "main"]));
}

#[test]
fn changing_selection_keeps_claims_for_unpublished_work() {
    let f = Fixture::new();
    cli(&f.root, &["install-hooks"]);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    cli(&lane, &["start", "g"]);
    work(&lane, "feature", "g");
    cli(&lane, &["start", "none"]);
    let other = f.clone("other");
    cli(&other, &["install-hooks"]);
    bad(&other, &["start", "g"], "claimed");
    cli(&lane, &["land", "--reason", "shared check"]);
    assert!(git(&lane, &["ls-remote", "origin", "refs/whosaidso/claims/*"]).is_empty());
}

#[test]
fn interrupted_unfolding_resumes_before_the_cleanliness_check() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    failure(
        command(&lane, BIN, &["land", "--reason", "shared check"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != before-push ]",
            )
            .output()
            .unwrap(),
        "before-push",
    );
    work(&f.root, "other", "none");
    git(&f.root, &["push", "origin", "main"]);
    let real = success(
        command(&lane, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = f.root.join(".git/fault-bin");
    executable(
        &bin.join("git"),
        "#!/bin/sh\nif [ \"$1\" = add ]; then\n for p in \"$@\"; do\n  if [ \"$p\" = .whosaidso/done/g.jsonl ]; then kill -KILL \"$PPID\"; exit 1; fi\n done\nfi\nexec \"$REAL_GIT\" \"$@\"\n",
    );
    let out = command(&lane, BIN, &["land", "--reason", "shared check"])
        .env("REAL_GIT", real.trim())
        .env(
            "PATH",
            format!("{}:{}", bin.display(), tool_path().to_string_lossy()),
        )
        .output()
        .unwrap();
    assert!(!out.status.success());
    assert!(!git(&lane, &["status", "--porcelain"]).is_empty());
    failure(
        command(&lane, BIN, &["land", "--reason", "shared check"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != unfold-committed ]",
            )
            .output()
            .unwrap(),
        "unfold-committed",
    );
    cli(&lane, &["land", "--reason", "shared check"]);
    assert!(git(&lane, &["status", "--porcelain"]).is_empty());
}

#[test]
fn prune_recovery_preserves_edited_notes_and_generated_bundles() {
    let f = Fixture::new();
    let note = f.root.join(".whosaidso/notes/choice.md");
    let original = "---\n{\"type\":\"idea\",\"status\":\"resolved\",\"title\":\"Choice\",\"goal\":\"g\"}\n---\noriginal\n";
    write(&note, original);
    commit(&f.root);
    r::proof(&f.root, &head(&f.root), 1, 0);
    commit(&f.root);
    let real = success(
        command(&f.root, "sh", &["-c", "command -v git"])
            .output()
            .unwrap(),
    );
    let bin = f.root.join(".git/fault-bin");
    executable(
        &bin.join("git"),
        "#!/bin/sh\nif [ -f \"$JOURNAL\" ] && grep -q notes/choice.md \"$JOURNAL\"; then kill -KILL \"$PPID\"; exit 1; fi\nexec \"$REAL_GIT\" \"$@\"\n",
    );
    assert!(
        !command(&f.root, BIN, &["prune"])
            .env("REAL_GIT", real.trim())
            .env("JOURNAL", f.root.join(".git/whosaidso/prune.journal"))
            .env(
                "PATH",
                format!("{}:{}", bin.display(), tool_path().to_string_lossy())
            )
            .output()
            .unwrap()
            .status
            .success()
    );
    let bundle = f.root.join(".whosaidso/done/g.jsonl");
    let encoded = std::fs::read(&bundle).unwrap();
    write(&note, original.replace("original", "owner edit"));
    bad(&f.root, &["prune"], "divergent edit");
    assert!(
        std::fs::read_to_string(&note)
            .unwrap()
            .contains("owner edit")
    );
    write(&note, original);
    write(&bundle, "owner bundle edit");
    bad(&f.root, &["prune"], "divergent edit");
    assert_eq!(
        std::fs::read_to_string(&bundle).unwrap(),
        "owner bundle edit"
    );
    write(&bundle, encoded);
    cli(&f.root, &["prune"]);
}

#[test]
fn landing_uses_the_selected_remotes_default_branch() {
    let f = Fixture::new();
    let remote = f.remote();
    let original = head(&f.root);
    git(&f.root, &["push", "origin", "main:master"]);
    git(
        &f.root,
        &["remote", "add", "upstream", remote.to_str().unwrap()],
    );
    git(&f.root, &["fetch", "upstream"]);
    git(
        &f.root,
        &[
            "symbolic-ref",
            "refs/remotes/origin/HEAD",
            "refs/remotes/origin/main",
        ],
    );
    git(
        &f.root,
        &[
            "symbolic-ref",
            "refs/remotes/upstream/HEAD",
            "refs/remotes/upstream/master",
        ],
    );
    let lane = f.worktree("worker");
    git(&lane, &["config", "branch.worker.remote", "upstream"]);
    git(
        &lane,
        &["config", "branch.worker.merge", "refs/heads/master"],
    );
    work(&lane, "feature", "g");
    cli(&lane, &["land", "--reason", "shared check"]);
    assert_eq!(git(&remote, &["rev-parse", "main"]).trim(), original);
    assert_eq!(git(&remote, &["rev-parse", "master"]).trim(), head(&lane));
}

#[test]
fn proving_uses_main_advanced_by_an_ordinary_pull() {
    let f = Fixture::new();
    r::goal(&f.root, "parent");
    let mut goal = value(&f.root.join(".whosaidso/goals/g.json"));
    goal["parent"] = serde_json::json!("parent");
    r::put(&f.root, "goals", "g", &goal);
    commit(&f.root);
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    cli(&lane, &["land", "--reason", "shared check"]);
    git(&f.root, &["pull", "--ff-only"]);
    let other = f.clone("other");
    work(&other, "new-baseline", "none");
    git(&other, &["push", "origin", "main"]);
    git(&f.root, &["pull", "--ff-only"]);
    let baseline = head(&f.root);
    cli(&lane, &["prove", "g", "--reason", "shared check"]);
    let proof = paths(&lane.join(".whosaidso/proofs"))
        .iter()
        .map(|p| value(p))
        .find(|p| p["commit"] == head(&lane))
        .unwrap();
    assert_eq!(proof["main"]["commit"], baseline);
}

#[test]
fn divergent_main_and_cached_destination_require_a_refresh() {
    let f = Fixture::new();
    f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    let other = f.clone("other");
    work(&other, "remote-change", "none");
    git(&other, &["push", "origin", "main"]);
    work(&f.root, "local-change", "none");
    failure(
        command(&lane, BIN, &["land", "--reason", "shared check"])
            .env(
                "WHOSAIDSO_LAND_HOOK",
                "[ \"$WHOSAIDSO_LAND_STEP\" != fetch ]",
            )
            .output()
            .unwrap(),
        "fetch",
    );
    work(&other, "newer-remote", "none");
    git(&other, &["push", "origin", "main"]);
    cli(&lane, &["prove", "g", "--reason", "shared check"]);
    let proof = value(&paths(&lane.join(".whosaidso/proofs"))[0]);
    assert_eq!(proof["main"]["commit"], head(&other));
    git(&lane, &["remote", "set-url", "origin", "unreachable"]);
    bad(&lane, &["prove", "g", "--reason", "shared check"], "git:");
}

#[test]
#[ignore = "manual ledger growth measurement"]
fn a_thousand_goals_and_ten_thousand_events_stay_bounded() {
    use serde_json::json;
    use std::{fs, time::Instant};
    let total = Instant::now();
    let f = Fixture::new();
    let mut ids = vec!["g".to_owned()];
    for n in 1..1000 {
        let id = format!("g{n:04}");
        let p = r::goal(&f.root, &id);
        let mut goal = value(&p);
        goal["parent"] = json!("g");
        goal["order"] = json!(n);
        r::put(&f.root, "goals", &id, &goal);
        ids.push(id);
    }
    commit(&f.root);
    let proof = r::proof(&f.root, &head(&f.root), 0, 0);
    let mut proof = value(&proof);
    for id in &ids {
        proof["goal"] = json!(id);
        for event in 0..10 {
            proof["ran_at"] = json!(event);
            r::put(&f.root, "proofs", "", &proof);
        }
    }
    assert_eq!(paths(&f.root.join(".whosaidso/proofs")).len(), 10000);
    commit(&f.root);
    eprintln!(
        "growth: inputs committed after {:.3}s",
        total.elapsed().as_secs_f64()
    );
    cli(&f.root, &["prune"]);
    eprintln!(
        "growth: collection complete after {:.3}s",
        total.elapsed().as_secs_f64()
    );
    commit(&f.root);
    assert_eq!(
        fs::read_dir(f.root.join(".whosaidso/goals"))
            .unwrap()
            .count(),
        0
    );
    assert_eq!(
        fs::read_dir(f.root.join(".whosaidso/proofs"))
            .unwrap()
            .count(),
        0
    );
    let bundle = fs::read_to_string(f.root.join(".whosaidso/done/g.jsonl")).unwrap();
    assert_eq!(bundle.lines().count(), 2000);
    let mut samples = Vec::new();
    for _ in 0..8 {
        let start = Instant::now();
        let text = cli(&f.root, &["goals"]);
        samples.push(start.elapsed().as_secs_f64() * 1000.0);
        assert!(text.starts_with("1000/1000 done"));
    }
    let cold = samples.remove(0);
    samples.sort_by(f64::total_cmp);
    eprintln!(
        "growth: 1000 goals, 10000 events -> 1 bundle, 1000 selected proofs; goals cold={cold:.3}ms warm median={:.3}ms max={:.3}ms",
        samples[3], samples[6]
    );
    assert!(
        samples[3] < 50.0,
        "warm goals median exceeded 50ms: {}",
        samples[3]
    );
}

#[test]
fn a_failed_push_retries_when_the_remote_destination_moved() {
    for resume in [false, true] {
        let f = Fixture::new();
        let remote = f.remote();
        let lane = f.worktree("worker");
        work(&lane, "feature", "g");
        if resume {
            failure(
                command(&lane, BIN, &["land", "--reason", "shared check"])
                    .env(
                        "WHOSAIDSO_LAND_HOOK",
                        "[ \"$WHOSAIDSO_LAND_STEP\" != before-push ]",
                    )
                    .output()
                    .unwrap(),
                "before-push",
            );
        }
        work(&f.root, "raced", "none");
        git(&f.root, &["push", "origin", "main:race"]);
        executable(
            &remote.join("hooks/update"),
            "#!/bin/sh\nif [ \"$1\" = refs/heads/main ] && [ ! -f moved ]; then\n touch moved\n git update-ref refs/heads/main refs/heads/race \"$2\" || exit 1\n echo 'destination advanced inside the update hook' >&2\n exit 1\nfi\n",
        );
        let text = cli(&lane, &["land", "--reason", "shared check"]);
        assert!(text.contains("testing again"), "{text}");
        assert_eq!(
            text.matches("ran: g pass").count(),
            if resume { 1 } else { 2 },
            "{text}"
        );
        assert!(lane.join("raced").is_file());
        assert_eq!(git(&remote, &["rev-parse", "main"]).trim(), head(&lane));
    }
}

#[test]
fn a_failed_push_reports_the_error_when_the_destination_did_not_move() {
    let f = Fixture::new();
    let remote = f.remote();
    let lane = f.worktree("worker");
    work(&lane, "feature", "g");
    let original = head(&f.root);
    executable(
        &remote.join("hooks/update"),
        "#!/bin/sh\nif [ \"$1\" = refs/heads/main ]; then echo 'publication policy refused' >&2; exit 1; fi\n",
    );
    let text = bad(
        &lane,
        &["land", "--reason", "shared check"],
        "publication policy refused",
    );
    assert!(!text.contains("testing again"), "{text}");
    assert_eq!(git(&remote, &["rev-parse", "main"]).trim(), original);
}

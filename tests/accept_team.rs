//! Release acceptance: independent publishers, out-of-order landings and rejected pushes.
mod common;
#[path = "common/command.rs"]
mod commands;
use commands::command;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{collections::BTreeMap, fs, path::{Path, PathBuf}, process::Output, time::{SystemTime, UNIX_EPOCH}};
type Bytes = BTreeMap<String, Vec<u8>>;
const GOALS: [&str; 3] = ["alpha", "beta", "gamma"];
struct Sandbox(PathBuf);
impl Sandbox {
    fn new() -> Self {
        let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("accept-team-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos())); fs::create_dir_all(&path).unwrap();
        Self(path)
    } }
impl Drop for Sandbox {
    fn drop(&mut self) { fs::remove_dir_all(&self.0).unwrap(); }
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap();
}
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn output(out: Output, code: i32, key: &str) -> String {
    let text = format!("{}{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr)); assert_eq!(out.status.code(), Some(code), "{text}"); assert!(text.contains(key), "missing {key:?}: {text}");
    text
}
fn git(root: &Path, args: &[&str]) -> String { output(command(root, "git", args).output().unwrap(), 0, "") }
fn cli(root: &Path, args: &[&str], key: &str) -> String {
    let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap(); assert!(out.stderr.is_empty(), "successful answers belong on stdout: {out:?}");
    output(out, 0, key)
}
fn commit(root: &Path, message: &str) {
    git(root, &["add", ".whosaidso", "."]); git(root, &["commit", "--allow-empty", "-m", message]); assert_eq!(git(root, &["status", "--porcelain"]), "");
}
fn records(root: &Path) -> Bytes {
    let mut result = Bytes::new();
    for kind in ["proofs", "asks", "media"] {
        if let Ok(entries) = fs::read_dir(root.join(".whosaidso").join(kind)) {
            for entry in entries {
                let path = entry.unwrap().path(); assert_eq!(path.extension().unwrap(), "json", "unexpected publication temporary"); let bytes = fs::read(&path).unwrap(); assert_eq!(path.file_stem().unwrap().to_str().unwrap(), hash(&bytes)); result.insert(format!(".whosaidso/{kind}/{}", path.file_name().unwrap().to_str().unwrap()), bytes);
            } }
    }
    result
}
fn keep(before: &Bytes, after: &Bytes) { for (path, bytes) in before { assert_eq!(after.get(path), Some(bytes), "lost or changed {path}"); } }
fn blobs(root: &Path) -> PathBuf { root.join(git(root, &["rev-parse", "--git-common-dir"]).trim()).join("whosaidso/blobs") }
fn checkpoint(root: &Path, states: [&str; 3], expected: &Bytes, local_media: &Bytes) {
    assert_eq!(&records(root), expected, "record bytes in {}", root.display());
    let blobs = if local_media.is_empty() { PathBuf::new() } else { blobs(root) };
    for (name, bytes) in local_media {
        assert_eq!(fs::read(blobs.join(name)).unwrap(), *bytes, "retained image {name}");
    }
    let tree = cli(root, &["goals"], "P1 Team");
    for (goal, state) in GOALS.into_iter().zip(states) {
        let details = cli(root, &["show", goal], &format!("state: {state}\n")); assert_eq!(details.lines().filter(|l| l.starts_with("state:")).collect::<Vec<_>>(), [format!("state: {state}")]); let row = tree.lines().find(|line| line.split_whitespace().nth(1) == Some(goal)).unwrap(); assert_eq!(row.split_whitespace().next(), Some(state), "{tree}");
    }
    let proof_count = expected.keys().filter(|p| p.starts_with(".whosaidso/proofs/")).count(); let checked = cli(root, &["check"], &format!("check: ok (1 phases, 3 goals, 2 instruments, {proof_count} proofs)")); let mut warnings = Vec::new();
    for goal in GOALS {
        let count = expected.iter().filter(|(p, b)| p.starts_with(".whosaidso/asks/") && serde_json::from_slice::<Value>(b).unwrap()["goal"] == goal).count();
        if count > 5 { warnings.push(format!("warning: goal {goal}: asks cap 5: count {count}; run whosaidso prune")); }
    }
    for (path, bytes) in expected {
        if path.starts_with(".whosaidso/media/") {
            let v: Value = serde_json::from_slice(bytes).unwrap(); let sha = v["media_sha256"].as_str().unwrap();
            if !local_media.contains_key(&format!("{sha}.png")) {
                warnings.push(format!("warning: media: missing media {sha}"));
            } }
    }
    let mut actual: Vec<_> = checked.lines().filter(|l| l.starts_with("warning:")).map(str::to_owned).collect(); actual.sort(); warnings.sort(); assert_eq!(actual, warnings, "only documented local-media/retention warnings");
}
// The retention oracle follows USAGE's (ran_at, record hash) ordering, without asking prune which evidence it intended to keep.
fn retained(all: &Bytes) -> Bytes {
    let mut result = all.clone();
    { let kind = "asks"; for goal in GOALS {
            let mut ranked: Vec<_> = all.iter().filter_map(|(path, bytes)| { let v: Value = serde_json::from_slice(bytes).unwrap();
                (path.starts_with(&format!(".whosaidso/{kind}/")) && v["goal"] == goal)
                    .then(|| (v["ran_at"].as_i64().unwrap(), path.clone()))
            }).collect(); ranked.sort_by(|a, b| b.cmp(a));
            for (_, path) in ranked.into_iter().skip(5) { result.remove(&path); }
        } }
    let mut ranked: Vec<_> = all.iter().filter_map(|(path, bytes)| { let v: Value = serde_json::from_slice(bytes).unwrap(); path.starts_with(".whosaidso/media/").then(|| (v["ran_at"].as_i64().unwrap(), path.clone())) }).collect();
    ranked.sort_by(|a,b| b.cmp(a)); for (_, path) in ranked.into_iter().skip(100) { result.remove(&path); }
    result
}
fn prune(root: &Path, before: &Bytes, states: [&str; 3], local: &Bytes) -> Bytes {
    let after = retained(before); let removed = |kind: &str| before.keys().filter(|p| p.starts_with(&format!(".whosaidso/{kind}/")) && !after.contains_key(*p)).count(); let line = format!("prune: 0 proofs, {} asks, {} media archived\n", removed("asks"), removed("media")); assert_eq!(cli(root, &["prune"], &line), line); checkpoint(root, states, &after, local);
    after
}
fn publish(root: &Path, goal: &str, states: [&str; 3], local: &mut Bytes, fail_cover: bool) -> Bytes {
    let mut expected = records(root);
    if fail_cover { write(&root.join(".whosaidso/cache/fail-cover"), b"cover failed"); }
    let answer = cli(root, &["prove", goal], &format!("prove: {goal} pass ")); assert_eq!(answer.lines().count(), 2); assert!(answer.lines().nth(1).unwrap().starts_with(&format!("prove: {goal} {} ", if fail_cover { "fail" } else { "pass" }))); let added = records(root); keep(&expected, &added); assert_eq!(added.len(), expected.len() + 2);
    let head = git(root, &["rev-parse", "HEAD"]);
    for (path, bytes) in added.iter().filter(|(p, _)| !expected.contains_key(*p)) {
        let v: Value = serde_json::from_slice(bytes).unwrap(); assert!(path.starts_with(".whosaidso/proofs/")); assert_eq!(v["goal"], goal); assert_eq!(v["commit"], head.trim()); assert_eq!(v["exit_code"], if v["instrument"] == "cover" && fail_cover { 1 } else { 0 }); assert_eq!(v["output_sha256"], hash(v["output_tail"].as_str().unwrap().as_bytes())); let result = if v["exit_code"] == 0 { "pass" } else { "fail" }; let sha = Path::new(path).file_stem().unwrap().to_str().unwrap(); assert!(answer.lines().any(|l| l == format!("prove: {goal} {result} {}", &sha[..12])));
    }
    expected = added; checkpoint(root, states, &expected, local);
    for n in 0..6 {
        let said = format!("{goal} decision {n}: preserve the engineering evidence");
        // All developers discuss alpha's integration. Concurrent landings must prune the merged asks again, even after each side already pruned.
        let asked = cli(root, &["ask", "alpha", &said], "ask: alpha "); let next = records(root);
        keep(&expected, &next);
        assert_eq!(next.len(), expected.len() + 1);
        let (path, bytes) = next.iter().find(|(p, _)| !expected.contains_key(*p)).unwrap();
        assert!(path.starts_with(".whosaidso/asks/"));
        let v: Value = serde_json::from_slice(bytes).unwrap();
        assert_eq!(v["said"], said);
        assert_eq!(v["goal"], "alpha");
        assert_eq!(v["commit"], git(root, &["rev-parse", "HEAD"]).trim());
        assert_eq!(asked, format!("ask: alpha {}\n", &hash(bytes)[..12]));
        expected = next;
        checkpoint(root, states, &expected, local);
        let image = common::png(format!("{goal} image {n}"));
        write(&root.join(".whosaidso/cache/shot.png"), &image);
        let published = cli(root, &["media", goal, ".whosaidso/cache/shot.png", &format!("{goal} image {n}")], &format!("media: {goal} ")); let next = records(root);
        keep(&expected, &next);
        assert_eq!(next.len(), expected.len() + 1);
        let (_, bytes) = next.iter().find(|(p, _)| !expected.contains_key(*p)).unwrap(); let v: Value = serde_json::from_slice(bytes).unwrap();
        assert_eq!(v["media_sha256"], hash(&image));
        assert_eq!(v["shows"], format!("{goal} image {n}"));
        assert_eq!(v["goal"], goal);
        assert_eq!(v["commit"], git(root, &["rev-parse", "HEAD"]).trim());
        assert_eq!(published, format!("media: {goal} {}\n", &hash(bytes)[..12]));
        local.insert(format!("{}.png", hash(&image)), image);
        expected = next;
        checkpoint(root, states, &expected, local);
    }
    commit(root, &format!("Publish {goal} evidence"));
    checkpoint(root, states, &expected, local);
    expected
}
#[test]
fn team_landings_retry_rejected_push_without_losing_evidence() {
    let sandbox = Sandbox::new(); let origin = sandbox.0.join("origin.git"); let team = sandbox.0.join("team");
    let alpha = sandbox.0.join("alpha"); let beta = sandbox.0.join("beta"); let other = sandbox.0.join("other");
    git(&sandbox.0, &["init", "--bare", origin.to_str().unwrap()]);
    git(&sandbox.0, &["clone", origin.to_str().unwrap(), team.to_str().unwrap()]);
    write(&team.join(".whosaidso/.gitignore"), "cache/\n");
    write(&team.join(".whosaidso/phases/p.json"), json!({"title":"Team","order":1}).to_string());
    for goal in GOALS {
        write(&team.join(format!(".whosaidso/goals/{goal}.json")), json!({"title":goal,"done_when":"check and cover pass","check":"test","broader":"p"}).to_string());
    }
    write(&team.join(".whosaidso/instruments/test.json"), json!({"run":["sh","-c","printf 'unit evidence\\n'"],"answers":"unit tests","gaps":[{"what":"integration","why":"unit checks are incomplete","covered_by":"cover"}]}).to_string());
    write(&team.join(".whosaidso/instruments/cover.json"), json!({"run":["sh","-c","printf 'coverage evidence\\n'; test ! -f .whosaidso/cache/fail-cover"],"answers":"integration"}).to_string());
    cli(&team, &["install-hooks"], "hooks: installed\n");
    cli(&team, &["start", "none"], "start: none\n");
    commit(&team, "Create engineering ledger");
    git(&team, &["push", "-u", "origin", "main"]);
    git(&sandbox.0, &["clone", origin.to_str().unwrap(), other.to_str().unwrap()]);
    cli(&other, &["install-hooks"], "hooks: installed\n");
    for root in [&team, &other] { git(root, &["config", "pull.rebase", "false"]); }
    git(&team, &["worktree", "add", "-b", "alpha", alpha.to_str().unwrap()]);
    git(&team, &["worktree", "add", "-b", "beta", beta.to_str().unwrap()]);
    assert_eq!(git(&team, &["worktree", "list", "--porcelain"]).lines().filter(|l| l.starts_with("worktree ")).count(), 3);
    let empty = Bytes::new();
    for root in [&team, &alpha, &beta, &other] { checkpoint(root, ["open"; 3], &empty, &empty); }
    // Proof order is alpha, beta, gamma. Landing order is beta, gamma, alpha.
    let mut team_media = Bytes::new(); let mut other_media = Bytes::new(); let mut history = Vec::new();
    cli(&alpha, &["start", "alpha"], "start: alpha\n");
    write(&alpha.join("alpha.txt"), "alpha implementation\n");
    commit(&alpha, "Implement alpha");
    assert!(git(&alpha, &["log", "-1", "--format=%B"]).contains("Goal: alpha"));
    checkpoint(&alpha, ["active", "open", "open"], &empty, &team_media);
    let alpha_records = publish(&alpha, "alpha", ["active", "open", "open"], &mut team_media, false);
    history.push((git(&alpha, &["rev-parse", "HEAD"]).trim().to_owned(), alpha_records));
    cli(&beta, &["start", "beta"], "start: beta\n");
    write(&beta.join("beta.txt"), "beta implementation\n");
    commit(&beta, "Implement beta");
    checkpoint(&beta, ["active", "active", "open"], &empty, &team_media);
    let beta_records = publish(&beta, "beta", ["active", "active", "open"], &mut team_media, true);
    history.push((git(&beta, &["rev-parse", "HEAD"]).trim().to_owned(), beta_records.clone()));
    git(&other, &["checkout", "-b", "gamma"]);
    cli(&other, &["start", "gamma"], "start: gamma\n");
    write(&other.join("gamma.txt"), "gamma implementation\n");
    commit(&other, "Implement gamma");
    checkpoint(&other, ["open", "open", "active"], &empty, &other_media);
    let gamma_records = publish(&other, "gamma", ["open", "open", "active"], &mut other_media, false);
    history.push((git(&other, &["rev-parse", "HEAD"]).trim().to_owned(), gamma_records.clone()));
    cli(&other, &["start", "none"], "start: none\n");
    git(&other, &["checkout", "main"]);
    git(&other, &["pull", "--no-edit"]);
    checkpoint(&other, ["open", "open", "active"], &empty, &other_media);
    git(&other, &["merge", "--no-ff", "--no-edit", "gamma"]);
    checkpoint(&other, ["open", "open", "done"], &gamma_records, &other_media);
    let gamma_kept = prune(&other, &gamma_records, ["open", "open", "done"], &other_media);
    commit(&other, "Prune gamma landing");
    // Beta's landing owner wins the race to origin while gamma is ready to push.
    git(&team, &["pull", "--no-edit"]);
    checkpoint(&team, ["active", "active", "open"], &empty, &team_media);
    git(&team, &["merge", "--no-ff", "--no-edit", "beta"]);
    checkpoint(&team, ["active", "failing", "open"], &beta_records, &team_media);
    let beta_kept = prune(&team, &beta_records, ["active", "failing", "open"], &team_media);
    commit(&team, "Prune beta landing");
    git(&team, &["push"]);
    output(command(&other, "git", &["push"]).output().unwrap(), 1, "[rejected]");
    checkpoint(&other, ["open", "open", "done"], &gamma_kept, &other_media);
    // USAGE recipe exactly: pull, prune again, commit deletions, push.
    git(&other, &["pull", "--no-edit"]);
    let mut combined = gamma_kept.clone(); combined.extend(beta_kept);
    checkpoint(&other, ["open", "failing", "done"], &combined, &other_media);
    combined = prune(&other, &combined, ["open", "failing", "done"], &other_media);
    commit(&other, "Prune after rejected push");
    git(&other, &["push"]);
    checkpoint(&other, ["open", "failing", "done"], &combined, &other_media);
    git(&team, &["pull", "--no-edit"]);
    checkpoint(&team, ["active", "failing", "done"], &combined, &team_media);
    git(&team, &["merge", "--no-ff", "--no-edit", "alpha"]);
    combined.extend(history[0].1.clone());
    checkpoint(&team, ["done", "failing", "done"], &combined, &team_media);
    combined = prune(&team, &combined, ["done", "failing", "done"], &team_media);
    commit(&team, "Prune alpha landing");
    git(&team, &["push"]);
    checkpoint(&team, ["done", "failing", "done"], &combined, &team_media);
    git(&other, &["pull", "--no-edit"]);
    combined = prune(&other, &combined, ["done", "failing", "done"], &other_media);
    commit(&other, "Finish team synchronization");
    git(&other, &["push"]);
    git(&team, &["pull", "--no-edit"]);
    for root in [&alpha, &beta] {
        git(root, &["pull", "--no-edit", "origin", "main"]);
        checkpoint(root, ["done", "failing", "done"], &combined, &team_media);
        output(command(root, env!("CARGO_BIN_EXE_whosaidso"), &["prune"]).output().unwrap(), 1, "prune: run on main after merging");
        checkpoint(root, ["done", "failing", "done"], &combined, &team_media);
    }
    let final_tree = cli(&team, &["goals"], "2/3 done");
    for root in [&team, &alpha, &beta, &other] {
        let local = if root == &other { &other_media } else { &team_media };
        checkpoint(root, ["done", "failing", "done"], &combined, local);
        assert_eq!(cli(root, &["goals"], "2/3 done"), final_tree);
        // Even intentionally pruned records remain byte-for-byte in Git history.
        let evidence: Vec<_> = history.iter().flat_map(|(tip, records)| records.iter().map(move |(path, bytes)| (format!("{tip}:{path}"), bytes))).collect();
        let requests = root.join(".whosaidso/cache/history-objects");
        write(&requests, evidence.iter().map(|(name, _)| format!("{name}\n")).collect::<String>());
        let out = command(root, "git", &["cat-file", "--batch"]).stdin(fs::File::open(requests).unwrap()).output().unwrap(); let mut rest = out.stdout.as_slice();
        for (_, bytes) in evidence {
            assert_eq!(out.status.code(), Some(0), "{out:?}");
            let end = rest.iter().position(|b| *b == b'\n').unwrap(); let header = std::str::from_utf8(&rest[..end]).unwrap();
            assert_eq!(header.split_whitespace().nth(1), Some("blob"));
            let size: usize = header.split_whitespace().nth(2).unwrap().parse().unwrap();
            assert_eq!(&rest[end + 1..end + 1 + size], bytes);
            rest = &rest[end + 2 + size..];
        }
    }
    assert_eq!(git(&team, &["rev-parse", "main"]), git(&other, &["rev-parse", "main"]));
    assert_eq!(git(&team, &["ls-remote", "origin", "refs/heads/main"]).split_whitespace().next(), Some(git(&team, &["rev-parse", "main"]).trim()));
}

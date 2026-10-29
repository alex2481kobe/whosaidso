#[path = "common/command.rs"] mod commands;
use commands::command;
use std::{collections::BTreeMap, fs, path::{Path, PathBuf}, time::{SystemTime, UNIX_EPOCH}};

fn run(p: &Path, program: &str, args: &[&str], code: i32, line: &str) -> String {
    let o = command(p, program, args).env("CARGO_NET_OFFLINE", "true").output().unwrap();
    let s = format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
    assert_eq!(o.status.code(), Some(code), "{args:?}: {s}");
    assert!(s.contains(line), "{args:?}: {s}"); s
}
fn git(p: &Path, args: &[&str]) { run(p, "git", args, 0, ""); }
fn cli(p: &Path, args: &[&str], line: &str) { run(p, env!("CARGO_BIN_EXE_whosaidso"), args, 0, line); }
fn write(p: &Path, s: &str) { fs::create_dir_all(p.parent().unwrap()).unwrap(); fs::write(p, s).unwrap(); }
fn evidence(p: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    let mut out = BTreeMap::new();
    for kind in ["proofs", "asks", "media"] { let d = p.join(".whosaidso").join(kind); if d.exists() { for e in fs::read_dir(d).unwrap() { let e = e.unwrap(); if e.path().extension().is_some_and(|x| x == "json") { out.insert(PathBuf::from(kind).join(e.file_name()), fs::read(e.path()).unwrap()); } } } } out
}
fn keep(p: &Path, expected: &BTreeMap<PathBuf, Vec<u8>>) { for (name, bytes) in expected { assert_eq!(&fs::read(p.join(".whosaidso").join(name)).unwrap(), bytes, "{}", name.display()); } }
fn states(p: &Path, expected: &[(&str, &str)]) {
    let out = run(p, env!("CARGO_BIN_EXE_whosaidso"), &["goals"], 0, "done");
    for (id, state) in expected { assert!(out.lines().any(|l| l.split_whitespace().collect::<Vec<_>>().windows(2).any(|w| w == [*state, *id])), "expected {state} {id}: {out}"); }
}
fn commit(p: &Path, message: &str) { git(p, &["add", "."]); git(p, &["commit", "-m", message, "-m", "Goal: none"]); }
#[test]
fn solo_landing_and_clone() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("wss-solo-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&root).unwrap(); git(&root, &["init", "--bare", "origin.git"]); git(&root, &["clone", "origin.git", "work"]); let p = root.join("work");
    write(&p.join("Cargo.toml"), "[package]\nname='quickstart'\nversion='0.1.0'\nedition='2021'\n"); write(&p.join("src/lib.rs"), "#[test] fn works() { assert_eq!(2+2,4); }\n"); write(&p.join(".gitignore"), "/target/\n"); run(&p, "cargo", &["generate-lockfile", "--offline"], 0, ""); commit(&p, "fixture");
    write(&p.join(".whosaidso/phases/p1.json"), "{\"title\":\"First phase\",\"order\":1}\n");
    write(&p.join(".whosaidso/instruments/test.json"), "{\"run\":[\"cargo\",\"test\"],\"answers\":\"Tests pass\"}\n");
    write(&p.join(".whosaidso/goals/first-goal.json"), "{\"title\":\"First goal\",\"done_when\":\"Tests pass\",\"check\":\"test\",\"broader\":\"p1\"}\n");
    cli(&p, &["check"], "check: ok (1 phases, 1 goals, 1 instruments, 0 proofs)"); cli(&p, &["install-hooks"], "hooks: installed"); commit(&p, "Add records"); cli(&p, &["start", "first-goal"], "start: first-goal"); states(&p, &[("first-goal", "open")]);
    cli(&p, &["prove", "first-goal"], "prove: first-goal pass"); let mut saved = evidence(&p); assert_eq!(saved.len(), 1); commit(&p, "quick proof"); states(&p, &[("first-goal", "done")]);
    write(&p.join(".whosaidso/phases/p2.json"), "{\"title\":\"Second phase\",\"order\":2}\n");
    write(&p.join("pass.sh"), "#!/bin/sh\necho passing\nexit 0\n"); write(&p.join("fail.sh"), "#!/bin/sh\necho failing\nexit 7\n");
    write(&p.join(".whosaidso/instruments/pass.json"), "{\"run\":[\"sh\",\"pass.sh\"],\"answers\":\"pass\"}\n");
    write(&p.join(".whosaidso/instruments/fail.json"), "{\"run\":[\"sh\",\"fail.sh\"],\"answers\":\"failure\"}\n");
    for (id, check) in [("merge-goal", "pass"), ("squash-goal", "fail"), ("ff-goal", "test")] { write(&p.join(format!(".whosaidso/goals/{id}.json")), &format!("{{\"title\":\"{id}\",\"done_when\":\"done\",\"check\":\"{check}\",\"broader\":\"p2\"}}\n")); }
    commit(&p, "more records"); git(&p, &["push", "-u", "origin", "main"]); states(&p, &[("first-goal", "done"), ("merge-goal", "open"), ("squash-goal", "open"), ("ff-goal", "open")]); keep(&p, &saved);
    git(&p, &["checkout", "-b", "merge-work"]); cli(&p, &["start", "merge-goal"], "start: merge-goal"); write(&p.join("merge.txt"), "merge\n"); git(&p, &["add", "merge.txt"]); git(&p, &["commit", "-m", "merge work"]); states(&p, &[("merge-goal", "active")]);
    git(&p, &["checkout", "main"]); git(&p, &["merge", "--no-ff", "merge-work", "-m", "Merge work"]); states(&p, &[("merge-goal", "landed")]); cli(&p, &["prove", "merge-goal"], "prove: merge-goal pass"); saved = evidence(&p); assert_eq!(saved.len(), 2); commit(&p, "merge proofs"); states(&p, &[("merge-goal", "done")]); keep(&p, &saved);
    git(&p, &["checkout", "-b", "squash-work"]); cli(&p, &["start", "squash-goal"], "start: squash-goal"); write(&p.join("squash.txt"), "squash\n"); git(&p, &["add", "squash.txt"]); git(&p, &["commit", "-m", "squash work"]); states(&p, &[("squash-goal", "active")]); git(&p, &["checkout", "main"]); git(&p, &["merge", "--squash", "squash-work"]); git(&p, &["commit", "-m", "squashed", "-m", "Goal: squash-goal"]); states(&p, &[("squash-goal", "landed")]); cli(&p, &["prove", "squash-goal"], "prove: squash-goal fail"); saved = evidence(&p); assert_eq!(saved.len(), 3); commit(&p, "failure proof"); states(&p, &[("squash-goal", "failing")]); keep(&p, &saved);
    git(&p, &["checkout", "-b", "ff-work"]); cli(&p, &["start", "ff-goal"], "start: ff-goal"); write(&p.join("ff.txt"), "ff\n"); git(&p, &["add", "ff.txt"]); git(&p, &["commit", "-m", "ff work"]); states(&p, &[("ff-goal", "active")]); git(&p, &["checkout", "main"]); git(&p, &["merge", "--ff-only", "ff-work"]); states(&p, &[("ff-goal", "landed")]); cli(&p, &["prove", "ff-goal"], "prove: ff-goal pass"); saved = evidence(&p); assert_eq!(saved.len(), 4); commit(&p, "ff proof");
    let all = [("first-goal", "done"), ("merge-goal", "done"), ("squash-goal", "failing"), ("ff-goal", "done")]; states(&p, &all); git(&p, &["pull", "--ff-only"]); cli(&p, &["prune"], "prune: 0 proofs, 0 asks, 0 media archived"); states(&p, &all); keep(&p, &saved); git(&p, &["push"]);
    git(&root, &["clone", "origin.git", "second"]); let second = root.join("second"); states(&second, &all); keep(&second, &saved); fs::remove_dir_all(root).unwrap();
}

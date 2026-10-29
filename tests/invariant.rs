#[path = "common/command.rs"] mod commands;
use commands::command;
mod common;
// A seeded operation model checks retention against records and Git objects, never the cache.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{cell::RefCell, collections::{BTreeMap, BTreeSet}, fs, path::{Path, PathBuf}, process::Output, time::{Duration, SystemTime, UNIX_EPOCH}};
const IMAGE_BYTES: usize = 64 * 1024;
const STEPS: usize = 200;
fn steps() -> usize { if std::env::var("WHOSAIDSO_LONG").as_deref() == Ok("1") { 2000 } else { STEPS } }
fn record_cap() -> usize { 50 + 6 + 4 * (1 + 5) + 100 + 4 * 6 * (1 + steps() * 4) }
fn cache_cap() -> u64 { 2 * 1024 * 1024 + (steps() as u64 * 8) * 4096 }
fn success(out: Output) -> String { assert!(out.status.success(), "{out:?}"); String::from_utf8(out.stdout).unwrap() }
fn git(root: &Path, args: &[&str]) -> String { success(command(root, "git", args).output().unwrap()) }
fn cli(root: &Path, args: &[&str]) -> String { success(command(root, env!("CARGO_BIN_EXE_whosaidso"), args).output().unwrap()) }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn paths(dir: &Path) -> Vec<PathBuf> { let mut p: Vec<_> = fs::read_dir(dir).map(|d| d.map(|e| e.unwrap().path()).collect()).unwrap_or_default(); p.sort(); p }
fn values(dir: &Path) -> Vec<(PathBuf, Value)> { paths(dir).into_iter().filter(|p| p.extension().is_some_and(|e| e == "json")).map(|p| { let v = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap(); (p, v) }).collect() }
fn snapshot(root: &Path) -> BTreeMap<PathBuf, Vec<u8>> {     let mut all = BTreeMap::new(); for p in paths(root) { if p.file_name().unwrap() == "cache" { continue; } if p.is_dir() { all.extend(snapshot(&p)); } else { all.insert(p.clone(), fs::read(p).unwrap()); } } all
}
thread_local! { static SERIAL: std::cell::Cell<u64> = const { std::cell::Cell::new(0) };
    static HASHES: RefCell<BTreeMap<(String, String), Option<String>>> = const { RefCell::new(BTreeMap::new()) };
    static MEDIA: RefCell<BTreeMap<String, BTreeSet<String>>> = const { RefCell::new(BTreeMap::new()) }; }
fn commit(root: &Path) {
    let n = SERIAL.with(|s| { let n = s.get(); s.set(n + 1); n }); git(root, &["add", "."]); git(root, &["commit", "--allow-empty", "-m", "model", "-m", &format!("Goal: g{}", n % 4)]);
}
fn hash(bytes: &[u8]) -> String { Sha256::digest(bytes).iter().map(|b| format!("{b:02x}")).collect() }
fn next(random: &mut u64) -> u64 { *random = random.wrapping_mul(6364136223846793005).wrapping_add(1442695040888963407); *random >> 32 }
fn image(root: &Path, serial: u64, goal: &str) { write(&root.join(".whosaidso/cache/shot.png"), common::png(vec![serial as u8; 1 + serial as usize % IMAGE_BYTES]));
    assert!(cli(root, &["media", goal, ".whosaidso/cache/shot.png", "model image"]).starts_with("media:")); }
fn protected(root: &Path, trees: &[PathBuf]) -> BTreeSet<String> {
    let mut images = Vec::new(); for tree in std::iter::once(root).chain(trees.iter().map(PathBuf::as_path)) { images.extend(values(&tree.join(".whosaidso/media")).into_iter().map(|(_, v)| v)); } let mut tips: BTreeSet<String> = git(root, &["for-each-ref", "--format=%(objectname)", "refs/heads", "refs/tags"]).lines().map(str::to_owned).collect();
    for tree in std::iter::once(root).chain(trees.iter().map(PathBuf::as_path)) { tips.insert(git(tree, &["rev-parse", "HEAD"]).trim().to_owned()); }
    let mut objects = BTreeSet::new(); for tip in tips { MEDIA.with(|cache| {
        objects.extend(cache.borrow_mut().entry(tip.clone()).or_insert_with(|| git(root, &["ls-tree", "-r", "--format=%(objectname)", &tip, "--", ".whosaidso/media/"]).lines().map(str::to_owned).collect()).iter().cloned());
    }); }
    let requests = root.join(".whosaidso/cache/protected-objects"); write(&requests, objects.into_iter().map(|s| format!("{s}\n")).collect::<String>()); let batch = command(root, "git", &["cat-file", "--batch"]).stdin(fs::File::open(requests).unwrap()).output().unwrap();
    assert!(batch.status.success()); let mut rest = batch.stdout.as_slice();
    while !rest.is_empty() {
        let end = rest.iter().position(|&b| b == b'\n').unwrap(); let len = std::str::from_utf8(&rest[..end]).unwrap().split_whitespace().nth(2).unwrap().parse::<usize>().unwrap();
        images.push(serde_json::from_slice(&rest[end + 1..end + 1 + len]).unwrap()); rest = &rest[end + 2 + len..];
    }
    images.into_iter().map(|v| format!("{}.{}", v["media_sha256"].as_str().unwrap(), v["ext"].as_str().unwrap())).collect()
}
fn prune(root: &Path, trees: &[PathBuf]) {
    let r = root.join(".whosaidso"); let blobs = root.join(".git/whosaidso/blobs"); let before = cli(root, &["goals"]); let records = snapshot(&r); let bytes = snapshot(&blobs); let dirty = !git(root, &["status", "--porcelain", "--untracked-files=all", "--", ".whosaidso"]).is_empty();
    if dirty {
        let out = command(root, env!("CARGO_BIN_EXE_whosaidso"), &["prune"]).output().unwrap();
        assert_eq!(out.status.code(), Some(1), "dirty prune must refuse: {out:?}");
        assert_eq!(snapshot(&r), records); assert_eq!(snapshot(&blobs), bytes); assert_eq!(cli(root, &["goals"]), before);
        commit(root);
    }
    let before = cli(root, &["goals"]); let shown: BTreeMap<_, _> = values(&r.join("goals")).iter().map(|(p, _)| { let id = p.file_stem().unwrap().to_str().unwrap().to_owned(); let show = cli(root, &["show", &id]); (id, show) }).collect(); let clean = command(root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]).output().unwrap().status.success(); let held = protected(root, trees);
    for p in paths(&blobs).into_iter().chain(paths(&blobs.join("trash"))) { if p.extension().is_some_and(|e| e == "png") { fs::File::options().write(true).open(p).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now() - Duration::from_secs(15 * 86400))).unwrap(); } }
    let goals: BTreeSet<_> = values(&r.join("goals")).into_iter().map(|(p, _)| p.file_stem().unwrap().to_str().unwrap().to_owned()).collect(); let mut required = BTreeSet::new(); let mut pending: Vec<_> = values(&r.join("goals")).into_iter().map(|(_, v)| v["check"].as_str().unwrap().to_owned()).collect();
    while let Some(id) = pending.pop() {
        if required.insert(id.clone()) { let v: Value = serde_json::from_slice(&fs::read(r.join(format!("instruments/{id}.json"))).unwrap()).unwrap();
            if let Some(gaps) = v["gaps"].as_array() { pending.extend(gaps.iter().map(|g| g["covered_by"].as_str().unwrap().to_owned())); }
        }
    }
    let main: BTreeSet<_> = git(root, &["rev-list", "refs/heads/main"]).lines().map(str::to_owned).collect(); let reachable: BTreeSet<_> = git(root, &["rev-list", "--branches", "--tags", "HEAD"]).lines().map(str::to_owned).collect(); let proofs = values(&r.join("proofs")); let mut latest: BTreeMap<_, (i64, &PathBuf)> = BTreeMap::new(); let mut commits: BTreeMap<_, (i64, &PathBuf)> = BTreeMap::new();
    for (p, v) in &proofs {
        let (goal, instrument, at) = (v["goal"].as_str().unwrap(), v["instrument"].as_str().unwrap(), v["commit"].as_str().unwrap()); let rank = (v["ran_at"].as_i64().unwrap(), p); commits.entry((goal, instrument, at)).and_modify(|r| *r = (*r).max(rank)).or_insert(rank); let current = fs::read(r.join(format!("instruments/{instrument}.json"))).ok().map(|b| hash(&b));
        let historic = HASHES.with(|cache| cache.borrow_mut().entry((at.to_owned(), instrument.to_owned())).or_insert_with(|| command(root, "git", &["show", &format!("{at}:.whosaidso/instruments/{instrument}.json")]).output().ok().filter(|o| o.status.success()).map(|o| hash(&o.stdout))).clone());
        if main.contains(at) && current.is_some() && current == historic { latest.entry((goal, instrument)).and_modify(|r| *r = (*r).max(rank)).or_insert(rank); }
    }
    let expected: BTreeSet<_> = proofs.iter().filter(|(p, v)| { let (goal, instrument, at) = (v["goal"].as_str().unwrap(), v["instrument"].as_str().unwrap(), v["commit"].as_str().unwrap());
        goals.contains(goal) && required.contains(instrument) && reachable.contains(at) &&
            if main.contains(at) { latest.get(&(goal, instrument)).is_some_and(|(_, selected)| *selected == p) }
            else { commits[&(goal, instrument, at)].1 == p }
    }).map(|(p, _)| p.clone()).collect();
    let mut recent = BTreeSet::new(); { let mut groups: BTreeMap<String, Vec<(i64, PathBuf)>> = BTreeMap::new();
        for (p, v) in values(&r.join("asks")) { if goals.contains(v["goal"].as_str().unwrap()) { groups.entry(v["goal"].as_str().unwrap().to_owned()).or_default().push((v["ran_at"].as_i64().unwrap(), p)); } }
        for group in groups.values_mut() { group.sort_by(|a,b| b.cmp(a)); recent.extend(group.iter().take(5).map(|(_, p)| p.clone())); }
    }
    let mut media = values(&r.join("media")); media.sort_by(|a,b| (b.1["ran_at"].as_i64().unwrap(), &b.0).cmp(&(a.1["ran_at"].as_i64().unwrap(), &a.0)));
    recent.extend(media.iter().take(100).filter(|(_, v)| goals.contains(v["goal"].as_str().unwrap())).map(|(p,_)| p.clone()));
    cli(root, &["prune"]); assert_eq!(cli(root, &["goals"]), before, "prune changes no derived goal state");
    for (id, show) in shown { assert_eq!(cli(root, &["show", &id]), show, "show survives prune: {id}"); }
    if clean { assert!(command(root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]).output().unwrap().status.success(), "prune preserves a clean check"); }
    assert_eq!(paths(&r.join("proofs")).into_iter().collect::<BTreeSet<_>>(), expected, "proof retention model");
    assert_eq!(values(&r.join("instruments")).into_iter().map(|(p,_)| p.file_stem().unwrap().to_str().unwrap().to_owned()).collect::<BTreeSet<_>>(), required, "instrument retention model");
    let actual: BTreeSet<_> = ["asks", "media"].iter().flat_map(|k| paths(&r.join(k))).collect(); assert_eq!(actual, recent, "newest asks per goal and media across ledger");
    for name in &held { let b = fs::read(blobs.join(name)).unwrap(); assert_eq!(hash(&b), name.split('.').next().unwrap(), "every ref and worktree retains bytes"); }
    // Static fixture caps include one landed selection and one proof per pair per live commit.
    let cap = record_cap(); let ledger: Vec<_> = records.keys().filter(|p| p.extension().is_some_and(|e| e == "json") && p.exists()).collect();
    assert!(ledger.len() <= cap); assert!(ledger.iter().map(|p| fs::metadata(p).unwrap().len()).sum::<u64>() <= cap as u64 * 65536);
    let blob_bytes = size(&blobs); assert!(blob_bytes <= bytes.values().map(|b| b.len() as u64).sum(), "prune only moves or removes bytes, including recoverable trash");
    assert!(paths(&blobs).iter().all(|p| !p.file_name().unwrap().to_string_lossy().starts_with(".tmp-")), "interrupted writes are collected");
    assert!(blob_bytes <= 4 * 1024 * 1024 * 1024, "image store quota includes trash");
    let cache_bytes = size(&r.join("cache")); assert!(cache_bytes <= cache_cap(), "static fixture cache bound"); let ledger_bytes: u64 = ledger.iter().map(|p| fs::metadata(p).unwrap().len()).sum();
    assert!(ledger_bytes + blob_bytes + cache_bytes <= record_cap() as u64 * 65536 + 4 * 1024 * 1024 * 1024 + cache_cap(), "static storage bound from caps");
    assert!(!snapshot(&r).keys().any(|p| p.file_name().unwrap().to_string_lossy().starts_with(".tmp-")), "record temporaries are collected");
    commit(root); }
fn size(dir: &Path) -> u64 { paths(dir).iter().map(|p| if p.is_dir() { size(p) } else { fs::metadata(p).unwrap().len() }).sum() }
fn copy(from: &Path, to: &Path) {
    fs::create_dir_all(to).unwrap();
    for path in paths(from) { let target = to.join(path.file_name().unwrap()); if path.is_dir() { copy(&path, &target); } else { fs::copy(path, target).unwrap(); } }
}
fn fixture(home: &Path) {
    fs::create_dir_all(&home).unwrap(); git(&home, &["init", "--bare", "origin.git"]); fs::create_dir_all(home.join("project")).unwrap(); git(&home.join("project"), &["init"]); git(&home.join("project"), &["remote", "add", "origin", "../origin.git"]);
    let root = home.join("project"); let r = root.join(".whosaidso"); write(&r.join(".gitignore"), "cache/\n"); write(&r.join("phases/p.json"), json!({"title":"Phase","order":1}).to_string());
    for n in 0..4 {
        let id = format!("g{n}"); let check = format!("i{n}");
        write(&r.join(format!("instruments/{check}.json")), json!({"run":["sh","-c",format!("test -f .whosaidso/cache/pass # {n}")],"answers":"works","gaps":[{"what":"coverage","why":"required","covered_by":"cover-a"}]}).to_string());
        let g = json!({"title":id,"done_when":"done","check":check,"broader":"p"}).to_string(); write(&r.join(format!("goals/{id}.json")), &g);
    }
    write(&r.join("instruments/cover-a.json"), json!({"run":["sh","-c","true # cover-a"],"answers":"coverage","gaps":[{"what":"nested","why":"required","covered_by":"cover-b"}]}).to_string());
    write(&r.join("instruments/cover-b.json"), json!({"run":["sh","-c","true # cover-b"],"answers":"nested"}).to_string());
    cli(&root, &["install-hooks"]); commit(&root); git(&root, &["push", "-u", "origin", "main"]);
 }
fn sequence(seed: u64, fixture: &Path) {
    SERIAL.with(|s| s.set(1)); eprintln!("invariant seed {seed:#x}"); let mut random = seed;
    let home = fixture.with_file_name(format!("invariant-{seed}-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos())); copy(fixture, &home); let root = home.join("project"); let r = root.join(".whosaidso");
    let mut catalog = BTreeMap::new(); let mut saved: BTreeMap<_, _> = values(&r.join("goals")).into_iter().map(|(p,v)| (p.file_stem().unwrap().to_str().unwrap().to_owned(), v.to_string())).collect(); let mut branches: Vec<String> = Vec::new(); let mut trees = Vec::new(); let mut order: Vec<usize> = (0..25).collect(); let steps = steps();
    for step in 0..steps {
        if step % 25 == 0 { for i in (1..25).rev() { let j = next(&mut random) as usize % (i + 1); order.swap(i, j); } }
        let n = next(&mut random); let id = format!("g{}", n % 4); let gp = r.join(format!("goals/{id}.json")); let live = values(&r.join("goals")); let live_id = live.first().map(|(p, _)| p.file_stem().unwrap().to_str().unwrap().to_owned());
        if step % 100 == 0 { eprintln!("seed {seed:#x} step {step}"); }
        match order[step % 25] {
            0 => { let mut v: Value = serde_json::from_str(&saved[&id]).unwrap(); v["title"] = format!("{id}-{step}").into(); let g = v.to_string(); write(&gp, &g); saved.insert(id.clone(), g); }
            1 => if let Some((_, g)) = live.first() { let p = r.join(format!("instruments/{}.json", g["check"].as_str().unwrap())); let mut v: Value = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap(); v["answers"] = step.to_string().into(); write(&p, v.to_string()); },
            2 => if let Some(g) = &live_id { assert!(cli(&root, &["start", g]).contains(g)); },
            3 | 4 => if let Some(g) = &live_id {
                commit(&root); if order[step % 25] == 3 { write(&r.join("cache/pass"), "pass"); } else { let _ = fs::remove_file(r.join("cache/pass")); }
                assert!(cli(&root, &["prove", g]).contains(if order[step % 25] == 3 { " pass " } else { " fail " }));
                if n % 2 == 0 {
                    let (_, mut v) = values(&r.join("proofs")).into_iter().max_by_key(|(_, v)| v["ran_at"].as_i64().unwrap()).unwrap();
                    v["output_tail"] = format!("tie-{step}").into(); v["ran_at"] = [i64::MIN, -1, 0, 42, i64::MAX, v["ran_at"].as_i64().unwrap()][n as usize % 6].into(); let bytes = serde_json::to_vec(&v).unwrap(); write(&r.join(format!("proofs/{}.json", hash(&bytes))), bytes);
                }
            },
            5 => commit(&root),
            6 => if let Some(g) = &live_id {
                commit(&root); let branch = format!("b{step}"); git(&root, &["checkout", "-b", &branch]); write(&root.join(format!("work-{step}")), "work"); commit(&root); cli(&root, &["prove", g]); image(&root, n, g); cli(&root, &["ask", g, "branch request"]); commit(&root); git(&root, &["push", "origin", &branch]); git(&root, &["checkout", "main"]); branches.push(branch);
            },
            7 => if let Some(branch) = branches.pop() {
                commit(&root); git(&root, &["merge", if n % 2 == 0 { "--no-ff" } else { "--squash" }, "--no-edit", &branch]); commit(&root); git(&root, &["push", "origin", "--delete", &branch]); git(&root, &["branch", "-D", &branch]);
            },
            8 => { commit(&root); git(&root, &["push", "origin", "main"]); }
            9 => if gp.exists() { fs::remove_file(&gp).unwrap(); },
            10 | 11 => { if gp.exists() { fs::remove_file(&gp).unwrap(); } commit(&root); write(&gp, &saved[&id]); if order[step % 25] == 11 { commit(&root); } }
            12 => if let Some((p, g)) = live.first() {
                let mut g = g.clone(); let old = g["check"].as_str().unwrap().to_owned(); let new = format!("i-{step}"); let bytes = fs::read(r.join(format!("instruments/{old}.json"))).unwrap(); write(&r.join(format!("instruments/{new}.json")), bytes); fs::remove_file(r.join(format!("instruments/{old}.json"))).unwrap();
                g["check"] = new.into(); let id = p.file_stem().unwrap().to_str().unwrap().to_owned(); saved.insert(id, g.to_string()); write(p, g.to_string()); commit(&root);
            },
            13 => if let Some(g) = &live_id { for i in 0..7 { image(&root, n + i, g); cli(&root, &["ask", g, &format!("request-{step}-{i}")]); } },
            14 => if let Some(g) = &live_id {
                commit(&root); let path = home.join(format!("tree-{step}")); git(&root, &["worktree", "add", "--detach", path.to_str().unwrap(), "HEAD"]); image(&path, n, g);
                if n % 2 == 0 { commit(&path); for p in paths(&path.join(".whosaidso/media")) { fs::remove_file(p).unwrap(); } }
                trees.push(path); if trees.len() > 2 { let p = trees.remove(0); git(&root, &["worktree", "remove", "--force", p.to_str().unwrap()]); } },
            15 => {
                write(&root.join(format!(".git/whosaidso/blobs/.tmp-{step}")), "interrupted");
                if let Some(g) = &live_id {
                    write(&r.join("cache/shot.png"), common::png([step as u8; IMAGE_BYTES]));
                    let out = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 0; exec \"$1\" media \"$2\" .whosaidso/cache/shot.png interrupted", "sh", env!("CARGO_BIN_EXE_whosaidso"), g]).output().unwrap();
                    assert!(!out.status.success(), "interrupted publication must fail");
                }
                #[cfg(debug_assertions)] {
                    commit(&root);
                    for hook in ["WHOSAIDSO_PRUNE_TOUCH_HOOK", "WHOSAIDSO_PRUNE_TRASH_HOOK", "WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"] {
                        for folder in [root.join(".git/whosaidso/blobs"), root.join(".git/whosaidso/blobs/trash")] { for p in paths(&folder) {
                            if p.extension().is_some_and(|e| e == "png") { fs::File::open(p).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now())).unwrap(); }
                        } }
                        let bytes = common::png(format!("orphan-{step}-{hook}")); let name = format!("{}.png", hash(&bytes)); let blob = root.join(".git/whosaidso/blobs").join(&name); write(&blob, &bytes);
                        fs::File::open(&blob).unwrap().set_times(fs::FileTimes::new().set_modified(SystemTime::now() - Duration::from_secs(15 * 86400))).unwrap();
                        let before = cli(&root, &["goals"]); let out = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prune"]).env(hook, "kill -KILL $PPID").output().unwrap();
                        use std::os::unix::process::ExitStatusExt; assert_eq!(out.status.signal(), Some(9));
                        let trash = blob.parent().unwrap().join("trash").join(&name); let kept = if blob.exists() { &blob } else { &trash };
                        assert_eq!(fs::read(kept).unwrap(), bytes); assert!(fs::metadata(kept).unwrap().modified().unwrap().elapsed().unwrap() < Duration::from_secs(60));
                        cli(&root, &["prune"]); assert_eq!(fs::read(if blob.exists() { &blob } else { &trash }).unwrap(), bytes); assert_eq!(cli(&root, &["goals"]), before); commit(&root);
                    }
                }
            }
            18 => { commit(&root); let tag = format!("tag-{}", step / 25 % 4); git(&root, &["tag", "-f", "-a", &tag, "-m", "held images"]); }
            19 => { commit(&root); let base = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); git(&root, &["checkout", "-b", "scratch"]); write(&root.join(format!("scratch-{step}")), "scratch"); commit(&root); let tip = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); git(&root, &["checkout", "main"]); write(&root.join(format!("onto-{step}")), "onto"); commit(&root); git(&root, &["cherry-pick", &tip]); git(&root, &["checkout", "scratch"]); git(&root, &["rebase", "--onto", "main", &base]); assert_eq!(git(&root, &["rev-parse", "HEAD"]), git(&root, &["rev-parse", "main"]), "rebased duplicate patch is dropped"); git(&root, &["reset", "--hard", &base]); assert_eq!(git(&root, &["rev-parse", "HEAD"]).trim(), base, "scratch rewind"); git(&root, &["checkout", "main"]); git(&root, &["branch", "-D", "scratch"]); }
            20 => if let Some(g) = &live_id {
                let out = command(&root, "sh", &["-c", "ulimit -c 0; ulimit -f 1; exec \"$1\" ask \"$2\" \"$3\"", "sh", env!("CARGO_BIN_EXE_whosaidso"), g, &"x".repeat(20000)]).output().unwrap();
                assert!(!out.status.success()); commit(&root);
            }
            21 => {
                commit(&root);
                #[cfg(debug_assertions)] {
                    let base = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); write(&root.join(format!("race-{step}")), "work"); commit(&root); let tip = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); let other = home.join("racer"); git(&root, &["worktree", "add", "--detach", other.to_str().unwrap(), "HEAD"]);
                    let action = format!("git -C '{}' -c user.name=Test -c user.email=test@example.com -c init.defaultBranch=main update-ref refs/heads/main {base}", other.display());
                    for hook in ["WHOSAIDSO_PRUNE_HOOK", "WHOSAIDSO_PRUNE_DELETE_HOOK"] {
                        let before = cli(&root, &["goals"]); let records = snapshot(&r); let bytes = snapshot(&root.join(".git/whosaidso/blobs")); let out = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["prune"]).env(hook, &action).output().unwrap();
                        if !out.status.success() { assert_eq!(snapshot(&r), records); assert_eq!(snapshot(&root.join(".git/whosaidso/blobs")), bytes); }
                        assert_eq!(git(&root, &["rev-parse", "refs/heads/main"]).trim(), base, "hook writes from another worktree");
                        git(&root, &["update-ref", "refs/heads/main", &tip]);
                        for (path, bytes) in records { write(&path, bytes); }
                        assert_eq!(cli(&root, &["goals"]), before, "local ref race restores selected proofs");
                    }
                    git(&root, &["worktree", "remove", "--force", other.to_str().unwrap()]);
                }
                assert!(cli(&root, &["find", "model"]).contains("shown")); }
            22 => {
                commit(&root); let tip = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); git(&root, &["update-ref", "refs/remotes/origin/main", &tip]); let before = cli(&root, &["goals"]); write(&root.join(format!("rewind-{step}")), "work"); commit(&root); git(&root, &["update-ref", "refs/remotes/origin/main", "HEAD"]); git(&root, &["reset", "--hard", &tip]); git(&root, &["update-ref", "refs/remotes/origin/main", &tip]);
                assert_eq!(git(&root, &["rev-parse", "HEAD"]), git(&root, &["rev-parse", "origin/main"])); assert_eq!(cli(&root, &["goals"]), before);
            }
            23 => if let Some(g) = &live_id {
                commit(&root); let base = git(&root, &["rev-parse", "HEAD"]).trim().to_owned(); git(&root, &["checkout", "-b", "carry"]); write(&root.join(format!("carry-{step}")), "work"); commit(&root); cli(&root, &["prove", g]); commit(&root); let evidence = snapshot(&r.join("proofs")); git(&root, &["checkout", "main"]); write(&root.join(format!("rebase-{step}")), "onto"); commit(&root);
                git(&root, &["checkout", "carry"]); git(&root, &["rebase", "--onto", "main", &base]);
                assert_eq!(snapshot(&r.join("proofs")), evidence, "rebase carries proof records unchanged");
                git(&root, &["checkout", "main"]); git(&root, &["merge", "--ff-only", "carry"]); git(&root, &["branch", "-D", "carry"]);
            },
            24 => {
                write(&r.join(format!("phases/p{step}.json")), json!({"title":"Growing catalogue","order":step + 2}).to_string());
                let phases = values(&r.join("phases")); if phases.len() > 50 { let out = command(&root, env!("CARGO_BIN_EXE_whosaidso"), &["check"]).output().unwrap();
                    assert!(!out.status.success() && String::from_utf8_lossy(&out.stdout).contains("phases cap 50"));
                    fs::remove_file(phases.iter().find(|(p, _)| p.file_stem().unwrap() != "p").unwrap().0.clone()).unwrap();
                }
                for i in 0..8 { write(&r.join(format!("instruments/unused-{step}-{i}.json")), json!({"run":["true",format!("unused-{step}-{i}")],"answers":"unused"}).to_string()); } },
            16 => prune(&root, &trees),
            17 => if let Some(g) = &live_id {
                commit(&root); let branch = format!("ff{step}"); git(&root, &["checkout", "-b", &branch]); write(&root.join(format!("fast-{step}")), "fast"); commit(&root); cli(&root, &["prove", g]); commit(&root); git(&root, &["checkout", "main"]); git(&root, &["merge", "--ff-only", &branch]); git(&root, &["branch", "-D", &branch]);
            },
            _ => (), }
        for (p, _) in values(&r.join("instruments")) { catalog.insert(p.file_stem().unwrap().to_str().unwrap().to_owned(), fs::read(p).unwrap()); }
        let mut pending: Vec<_> = values(&r.join("goals")).into_iter().map(|(_,v)| v["check"].as_str().unwrap().to_owned()).collect(); let mut seen = BTreeSet::new();
        while let Some(id) = pending.pop() { if seen.insert(id.clone()) {
            let p = r.join(format!("instruments/{id}.json")); if !p.exists() { write(&p, &catalog[&id]); } let v: Value = serde_json::from_slice(&fs::read(&p).unwrap()).unwrap(); if let Some(gaps) = v["gaps"].as_array() { pending.extend(gaps.iter().map(|g| g["covered_by"].as_str().unwrap().to_owned())); }
        } }
        if step % 25 == 24 { prune(&root, &trees); }
    }
    prune(&root, &trees); assert!(cli(&root, &["check"]).contains("check: ok")); eprintln!("invariant seed {seed:#x}: {steps} steps passed"); fs::remove_dir_all(home).unwrap();
}
#[test]
fn seeded_prune_invariants() {
    let template = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("invariant-fixture-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos())); fixture(&template); let seeds = [0x5001, 0x5002, 0x5003, 0x5004, 0x5009, 0x12345678, 0xdeadbeef, 0x602810, 0xf00d6006, 0xabcdef, 0x701dead, 0x702beef, 0xbadcafe, 0xaaa77];
    let every = std::env::var("WHOSAIDSO_LONG").as_deref() == Ok("1") || std::env::var("WHOSAIDSO_SEEDS").as_deref() == Ok("all");
    let seeds = if every { &seeds[..] } else { &seeds[..2] }; let template_ref = &template;
    std::thread::scope(|scope| {
        let runs: Vec<_> = seeds.iter().map(|&seed| (seed, scope.spawn(move || sequence(seed, template_ref)))).collect();
        for (seed, run) in runs { assert!(run.join().is_ok(), "invariant failed with seed {seed:#x}"); }
    });
    fs::remove_dir_all(template).unwrap();
}

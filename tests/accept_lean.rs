// Independent acceptance attack on the 0.3.0 release candidate: claims, the viewer's change token, docs and the gate.
// Every test builds its own bare origin and clones under target/e2e-tmp and runs the real binary.
use serde_json::{Value, json};
use std::{fs, io::{BufRead, BufReader, Read, Write}, net::TcpStream, path::{Path, PathBuf}, process::{Child, Command, Output, Stdio}, time::{SystemTime, UNIX_EPOCH}};
use std::time::Duration;

const BIN: &str = env!("CARGO_BIN_EXE_whosaidso");
fn now() -> u64 { SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_secs() }
fn command(dir: &Path, home: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(dir).env("HOME", home).env("WHOSAIDSO_HOME", home.join("registry")).env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CEILING_DIRECTORIES", home.parent().unwrap()).env_remove("WHOSAIDSO_BLOBS").env_remove("WHOSAIDSO_ARCHIVE");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
struct Lab { home: PathBuf }
impl Lab {
    fn new(name: &str) -> Self {
        let home = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("lean-{name}-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
        fs::create_dir_all(&home).unwrap(); Self { home }
    }
    fn out(&self, dir: &Path, program: &str, args: &[&str]) -> Output { command(dir, &self.home, program, args).output().unwrap() }
    fn ok(&self, dir: &Path, program: &str, args: &[&str]) -> String {
        let o = self.out(dir, program, args);
        assert!(o.status.success(), "{program} {args:?}: {} {}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
        String::from_utf8(o.stdout).unwrap()
    }
    fn git(&self, dir: &Path, args: &[&str]) -> String { self.ok(dir, "git", args) }
    fn cli(&self, dir: &Path, args: &[&str]) -> String { self.ok(dir, BIN, args) }
    fn fail(&self, dir: &Path, args: &[&str]) -> String {
        let o = self.out(dir, BIN, args);
        assert!(!o.status.success(), "{args:?} should be refused: {o:?}");
        format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr))
    }
    // A project with one phase, one passing instrument and the given goals, committed on main with hooks installed.
    fn project(&self, dir: &Path, goals: &[&str]) {
        fs::create_dir_all(dir).unwrap();
        let r = dir.join(".whosaidso");
        write(&r.join(".gitignore"), "cache/\n");
        write(&r.join("phases/p.json"), json!({"title":"Phase","order":1}).to_string());
        write(&r.join("instruments/test.json"), json!({"run":["true"],"answers":"works"}).to_string());
        for g in goals { write(&r.join(format!("goals/{g}.json")), json!({"title":g,"done_when":"works","check":"test","broader":"p"}).to_string()); }
        self.cli(dir, &["install-hooks"]);
    }
    // A bare origin with clone `a` holding the records, and further clones of it.
    fn team(&self, goals: &[&str], clones: &[&str]) -> Vec<PathBuf> {
        let origin = self.home.join("origin.git");
        self.git(&self.home, &["init", "--bare", "origin.git"]);
        let a = self.home.join("a");
        self.git(&self.home, &["clone", "-q", origin.to_str().unwrap(), "a"]);
        self.project(&a, goals);
        self.git(&a, &["add", "."]); self.git(&a, &["commit", "-q", "-m", "records"]); self.git(&a, &["push", "-q", "-u", "origin", "main"]);
        let mut all = vec![a];
        for name in clones {
            self.git(&self.home, &["clone", "-q", origin.to_str().unwrap(), name]);
            let c = self.home.join(name); self.cli(&c, &["install-hooks"]); all.push(c);
        }
        all
    }
}
impl Drop for Lab { fn drop(&mut self) { if !std::thread::panicking() { let _ = fs::remove_dir_all(&self.home); } } }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn commit_work(lab: &Lab, dir: &Path, file: &str, goal: &str) {
    write(&dir.join(file), file); lab.git(dir, &["add", file]); lab.git(dir, &["commit", "-q", "-m", file, "-m", &format!("Goal: {goal}")]);
}

// ---- Claim 1: one holder per goal ----

// PIN (passes). Expects: when three clones run `start <goal>` at the same instant exactly one succeeds, origin ends with one claim ref,
// and releasing it frees the goal. No other test races `start`; the lease push in claims.rs is what makes this true.
#[test]
fn racing_starts_in_three_clones_yield_exactly_one_holder_per_goal() {
    let lab = Lab::new("race");
    let goals = ["g0", "g1", "g2", "g3", "g4", "g5", "g6", "g7"];
    let clones = lab.team(&goals, &["b", "c"]);
    // Control: a lone start succeeds and names the holder to a second clone.
    assert_eq!(lab.cli(&clones[0], &["start", "g0"]), "start: g0\n");
    assert!(lab.fail(&clones[1], &["start", "g0"]).contains("claimed by"), "control: a held goal is refused");
    lab.cli(&clones[0], &["start", "none"]);
    for goal in goals.iter().skip(1) {
        let handles: Vec<_> = clones.iter().map(|c| { let (c, goal, home) = (c.clone(), goal.to_string(), lab.home.clone()); std::thread::spawn(move || command(&c, &home, BIN, &["start", &goal]).output().unwrap()) }).collect();
        let outs: Vec<Output> = handles.into_iter().map(|h| h.join().unwrap()).collect();
        let winners = outs.iter().filter(|o| o.status.success()).count();
        assert_eq!(winners, 1, "exactly one of three racing clones may hold {goal}: {outs:?}");
        // Origin agrees: one claim ref for the goal, held by the winner's published blob.
        let refs = lab.git(&lab.home.join("origin.git"), &["for-each-ref", &format!("refs/whosaidso/claims/{goal}")]);
        assert_eq!(refs.lines().count(), 1, "{refs}");
        for c in &clones { lab.cli(c, &["start", "none"]); }
        assert_eq!(lab.git(&lab.home.join("origin.git"), &["for-each-ref", &format!("refs/whosaidso/claims/{goal}")]), "", "releasing must clear origin so the goal is free");
    }
}

// Writes a claim blob and ref directly, as a worker that last ran `start` `hours` ago would have published it.
fn plant_claim(lab: &Lab, repo: &Path, goal: &str, hours: u64, worker: &str, origin: bool) {
    let body = format!("worker {worker}\nbranch ghost\npath /ghost/tree\nauthor Ghost\nsince {}\n", now() - hours * 3600);
    let file = lab.home.join(format!("claim-{worker}.txt")); fs::write(&file, body).unwrap();
    let oid = lab.git(repo, &["hash-object", "-w", file.to_str().unwrap()]).trim().to_owned();
    lab.git(repo, &["update-ref", &format!("refs/whosaidso/claims/{goal}"), &oid]);
    if origin { lab.git(repo, &["push", "-q", "origin", &format!("refs/whosaidso/claims/{goal}")]); }
}


// PIN (passes). Expects: a claim 71 hours old still refuses (naming branch, author and path), one 73 hours old binds nothing (USAGE.md `start`).
// No other test covers the claim lapse.
#[test]
fn a_claim_lapses_exactly_after_seventy_two_hours() {
    let lab = Lab::new("lapse");
    let t = lab.team(&["young", "old"], &["b"]);
    let (a, b) = (&t[0], &t[1]);
    plant_claim(&lab, a, "young", 71, "ghost-young", true);
    plant_claim(&lab, a, "old", 73, "ghost-old", true);
    let refused = lab.fail(b, &["start", "young"]);
    assert!(refused.contains("ghost") && refused.contains("Ghost") && refused.contains("/ghost/tree"), "refusal must name branch, author and path: {refused}");
    assert_eq!(lab.cli(b, &["start", "old"]), "start: old\n", "a 73-hour-old claim has lapsed");
}



// DEFECT. Expects: a refs/whosaidso/claims/<goal> ref that does not hold a claim blob (hand-made or damaged) is ignored, not fatal.
// Why it matters: `start` fails with a raw `git cat-file ... bad file` for every worker on every clone, and no command can clear the ref.
// Root cause: claims::read propagates the cat-file error with `?`. Tolerating that alone is not enough: `expected` for the lease is also taken from read(),
// so the push would still be rejected as "just claimed by another worker". The lease must use the ref's real value.
#[test]
fn a_malformed_claim_ref_on_origin_does_not_block_the_goal() {
    let lab = Lab::new("garbage");
    let t = lab.team(&["g"], &["b"]);
    let (a, b) = (&t[0], &t[1]);
    // A claim ref that points at a commit, not a claim blob (a hand-made or damaged ref).
    let head = lab.git(a, &["rev-parse", "HEAD"]).trim().to_owned();
    lab.git(a, &["push", "-q", "origin", &format!("{head}:refs/whosaidso/claims/g")]);
    let out = lab.out(b, BIN, &["start", "g"]);
    assert!(out.status.success(), "a ref that holds no claim is no claim; start must proceed, got {}", String::from_utf8_lossy(&out.stderr));
}

// ---- Claim 3: the viewer ----

struct Viewer { child: Child, address: String, launch: String, cookie: String }
impl Viewer {
    fn start(lab: &Lab, root: &Path) -> Self {
        let mut child = command(root, &lab.home, BIN, &["ui", "--port", "0"]).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
        let mut url = String::new(); BufReader::new(child.stdout.take().unwrap()).read_line(&mut url).unwrap();
        assert!(url.starts_with("http://127.0.0.1:"), "viewer startup: {url}");
        let (address, launch) = url.trim().strip_prefix("http://").unwrap().split_once('/').unwrap();
        let mut v = Self { child, address: address.into(), launch: format!("/{launch}"), cookie: String::new() };
        let (status, header, _) = v.get(&v.launch.clone());
        assert_eq!(status, 303);
        v.cookie = header.lines().find_map(|l| l.strip_prefix("Set-Cookie: ")).unwrap().split(';').next().unwrap().into();
        v
    }
    fn get(&self, path: &str) -> (u16, String, Vec<u8>) {
        let mut stream = TcpStream::connect(&self.address).unwrap(); stream.set_read_timeout(Some(Duration::from_secs(20))).unwrap();
        write!(stream, "GET {path} HTTP/1.1\r\nHost: {}\r\nCookie: {}\r\nConnection: close\r\n\r\n", self.address, self.cookie).unwrap();
        let mut bytes = Vec::new(); stream.read_to_end(&mut bytes).unwrap();
        let split = bytes.windows(4).position(|b| b == b"\r\n\r\n").unwrap();
        let header = String::from_utf8(bytes[..split].to_vec()).unwrap();
        (header.split_whitespace().nth(1).unwrap().parse().unwrap(), header, bytes[split + 4..].to_vec())
    }
    fn json(&self, path: &str) -> Value { let (s, _, b) = self.get(path); assert_eq!(s, 200, "{path}: {}", String::from_utf8_lossy(&b)); serde_json::from_slice(&b).unwrap() }
    fn version(&self) -> String { self.json("/api/version")["version"].as_str().unwrap().to_owned() }
    fn goal(&self, id: &str) -> Value { self.json("/api/goals")["goals"].as_array().unwrap().iter().find(|g| g["id"] == id).unwrap().clone() }
}
impl Drop for Viewer { fn drop(&mut self) { let _ = self.child.kill(); let _ = self.child.wait(); } }

// One clone with main, records and goals `g`, `h`; branch `work` carries unlanded work on g.
fn with_branch(lab: &Lab, name: &str) -> PathBuf {
    let root = lab.home.join(name);
    lab.git(&lab.home, &["init", "-q", name]);
    lab.project(&root, &["g", "h"]);
    lab.git(&root, &["add", "."]); lab.git(&root, &["commit", "-q", "-m", "records"]);
    lab.git(&root, &["checkout", "-q", "-b", "work"]); commit_work(lab, &root, "w.txt", "g"); lab.git(&root, &["checkout", "-q", "main"]);
    root
}

// DEFECT. Expects: /api/goals reports the same worktree path that `whosaidso goals` prints, right after `git worktree add`.
// Why it matters: Goals promises the local worktree path of each branch, but the cached answer is keyed by a token that never sees worktree changes,
// so the viewer stays wrong (and never refreshes) until some ref or record happens to change.
// Root cause: version::token hashes refs and record folders only; state::worktrees (git worktree list) is read into the cached answer but not into the token.
#[test]
fn the_viewer_shows_a_worktree_added_after_it_started() {
    let lab = Lab::new("worktree");
    let root = with_branch(&lab, "proj");
    let viewer = Viewer::start(&lab, &root);
    assert_eq!(viewer.goal("g")["branches"][0]["name"], "work", "control: the branch is listed");
    assert_eq!(viewer.goal("g")["branches"][0]["worktree"], Value::Null, "control: nobody has it checked out yet");
    let before = viewer.version();
    let tree = lab.home.join("work-tree");
    lab.git(&root, &["worktree", "add", "-q", tree.to_str().unwrap(), "work"]);
    // The CLI answers at once; the viewer must too: Goals promises "branches (name, author, age, local worktree path)".
    assert!(lab.cli(&root, &["goals"]).contains(tree.to_str().unwrap()), "control: goals prints the new worktree");
    let shown = viewer.goal("g")["branches"][0]["worktree"].clone();
    assert_eq!(shown, json!(tree), "the viewer's cached Goals answer still has no worktree path ({shown}); version token {before} did not move: {}", viewer.version());
}

// DEFECT. Expects: /api/version changes when a gallery entry flips from missing to present (what `whosaidso sync` does by writing bytes).
// Why it matters: the Gallery JS keys cards by id+status precisely to redraw on this flip, but the poll never sees it, so an open page shows "missing" until reload.
// Root cause: version::token covers refs, record folders and the archive folder, not the blob store that api::media reads for `status`.
#[test]
fn the_version_moves_when_media_bytes_arrive() {
    let lab = Lab::new("bytes");
    let root = with_branch(&lab, "proj");
    // A media record whose bytes are not here yet (as after a fetch, before `sync`).
    let pdf = b"%PDF-1.7\n%%EOF\n";
    let src = lab.home.join("shot.pdf"); fs::write(&src, pdf).unwrap();
    lab.cli(&root, &["media", "g", src.to_str().unwrap(), "evidence"]);
    let blob = fs::read_dir(root.join(".git/whosaidso/blobs")).unwrap().map(|e| e.unwrap().path()).find(|p| p.extension().is_some_and(|e| e == "pdf")).unwrap();
    let held = lab.home.join("held.pdf"); fs::rename(&blob, &held).unwrap();
    let viewer = Viewer::start(&lab, &root);
    assert_eq!(viewer.json("/api/gallery")["entries"][0]["status"], "missing", "control: bytes are absent");
    let before = viewer.version();
    fs::rename(&held, &blob).unwrap(); // what `whosaidso sync` does when bytes are received
    assert_eq!(viewer.json("/api/gallery")["entries"][0]["status"], "present", "control: the gallery answer follows the bytes");
    assert_ne!(viewer.version(), before, "the gallery changed (missing -> present) but the poll token did not, so an open page never refreshes");
}

// DEFECT. Expects: a claim older than 72 hours is not shown as held (USAGE.md: it lapses; `start` ignores it, as the control line proves).
// Why it matters: the viewer tells a reader a goal is taken when anyone can take it. The same gap means a live claim that lapses while the viewer runs is never un-shown.
// Root cause: claims::held lists every claim ref and applies neither the 72-hour lapse nor the landed rule that claims::claim applies.
#[test]
fn the_viewer_does_not_show_a_lapsed_claim_as_held() {
    let lab = Lab::new("lapsedview");
    let root = with_branch(&lab, "proj");
    plant_claim(&lab, &root, "h", 100, "ghost-h", false);
    plant_claim(&lab, &root, "g", 1, "ghost-g", false);
    let viewer = Viewer::start(&lab, &root);
    assert_eq!(viewer.goal("g")["claim"]["author"], "Ghost", "control: a live claim is shown");
    // USAGE.md: a claim lapses 72 hours after its holder last ran start; `start h` ignores this one.
    assert_eq!(lab.cli(&root, &["start", "h"]), "start: h\n", "control: the 100-hour-old claim no longer binds");
    plant_claim(&lab, &root, "h", 100, "ghost-h", false);
    assert_eq!(viewer.goal("h")["claim"], Value::Null, "the viewer shows a claim that lapsed 100 hours ago as held");
}

// DEFECT. Expects: a claim on a landed goal is not shown as held (USAGE.md: it binds nothing once the goal has landed; `start` already lets a new worker take it).
// Root cause: same as the lapsed case; goals_screen joins claims::held to every goal without the settled check hooks::start uses.
#[test]
fn the_viewer_does_not_show_a_claim_on_a_landed_goal_as_held() {
    let lab = Lab::new("landedview");
    let root = with_branch(&lab, "proj");
    plant_claim(&lab, &root, "g", 1, "ghost-g", false);
    let viewer = Viewer::start(&lab, &root);
    assert_eq!(viewer.goal("g")["claim"]["author"], "Ghost", "control: the claim is shown while g is unlanded");
    lab.git(&root, &["merge", "-q", "--no-ff", "work", "-m", "land"]);
    let goal = viewer.goal("g");
    assert_eq!(goal["state"], "landed", "control");
    // USAGE.md: a claim binds nothing once the goal has landed.
    assert_eq!(goal["claim"], Value::Null, "a claim that binds nothing is shown as held on a landed goal");
}




// ---- Claim 5: gate ----

// PIN (passes). Expects: `gate` refuses while any phase, goal, instrument, proof, ask or media record is untracked, edited, deleted or only staged, and passes again
// once undone. No other test reaches the uncommitted-records refusal in gate.rs.
#[test]
fn gate_refuses_every_kind_of_uncommitted_record() {
    let lab = Lab::new("gate");
    let root = lab.home.join("proj"); lab.git(&lab.home, &["init", "-q", "proj"]);
    lab.project(&root, &["g", "h"]);
    lab.git(&root, &["add", "."]); lab.git(&root, &["commit", "-q", "-m", "records"]);
    lab.cli(&root, &["start", "g"]);
    lab.git(&root, &["checkout", "-q", "-b", "work"]); commit_work(&lab, &root, "w.txt", "g");
    assert!(lab.cli(&root, &["gate"]).starts_with("gate: ok (1 commits)"), "control: committed work with a Goal trailer passes");
    let shot = lab.home.join("shot.pdf"); fs::write(&shot, b"%PDF-1.7\n%%EOF\n").unwrap();
    let cases: Vec<(&str, Box<dyn Fn() + '_>, Box<dyn Fn() + '_>)> = vec![
        ("untracked ask", Box::new(|| { lab.cli(&root, &["ask", "g", "why"]); }), Box::new(|| { for f in fs::read_dir(root.join(".whosaidso/asks")).unwrap() { fs::remove_file(f.unwrap().path()).unwrap(); } })),
        ("untracked proof", Box::new(|| { lab.cli(&root, &["prove", "g"]); }), Box::new(|| { for f in fs::read_dir(root.join(".whosaidso/proofs")).unwrap() { fs::remove_file(f.unwrap().path()).unwrap(); } })),
        ("untracked media", Box::new(|| { lab.cli(&root, &["media", "g", shot.to_str().unwrap(), "shot"]); }), Box::new(|| { for f in fs::read_dir(root.join(".whosaidso/media")).unwrap() { fs::remove_file(f.unwrap().path()).unwrap(); } })),
        ("edited goal", Box::new(|| write(&root.join(".whosaidso/goals/h.json"), json!({"title":"edited","done_when":"works","check":"test","broader":"p"}).to_string())), Box::new(|| { lab.git(&root, &["checkout", "--", ".whosaidso/goals/h.json"]); })),
        ("deleted goal", Box::new(|| fs::remove_file(root.join(".whosaidso/goals/h.json")).unwrap()), Box::new(|| { lab.git(&root, &["checkout", "--", ".whosaidso/goals/h.json"]); })),
        ("new phase", Box::new(|| write(&root.join(".whosaidso/phases/q.json"), json!({"title":"Q","order":2}).to_string())), Box::new(|| fs::remove_file(root.join(".whosaidso/phases/q.json")).unwrap())),
        ("staged only", Box::new(|| { write(&root.join(".whosaidso/phases/q.json"), json!({"title":"Q","order":2}).to_string()); lab.git(&root, &["add", ".whosaidso/phases/q.json"]); }), Box::new(|| { lab.git(&root, &["rm", "-q", "-f", ".whosaidso/phases/q.json"]); })),
    ];
    for (name, make, undo) in &cases {
        make();
        let o = lab.out(&root, BIN, &["gate"]);
        let text = format!("{}{}", String::from_utf8_lossy(&o.stdout), String::from_utf8_lossy(&o.stderr));
        assert!(!o.status.success() && text.contains("uncommitted"), "gate must refuse with an {name}: {text}");
        undo();
        assert!(lab.out(&root, BIN, &["gate"]).status.success(), "gate passes again after undoing {name}");
    }
}


// ---- Claim 6: the documented flow ----






// DEFECT. Expects: the README flow (quick start records without a .gitignore, `start`, work on a branch, land on main) leaves `prune` able to run.
// Why it matters: `start` (and find, grep) silently creates an untracked .whosaidso/.gitignore via cache::dir, and prune refuses any untracked file in
// .whosaidso that is not a temporary record, with a message about "your .whosaidso changes". The lander is stopped on the first landing.
// Root cause: cache::dir writes a tracked-path file as a side effect; nothing tells the user to commit it and prune does not exempt it.
#[test]
fn the_documented_flow_leaves_nothing_that_blocks_prune() {
    let lab = Lab::new("flow");
    let root = lab.home.join("proj"); lab.git(&lab.home, &["init", "-q", "proj"]);
    // README "Quick start": records are written without a .gitignore, committed, and hooks installed.
    let r = root.join(".whosaidso");
    write(&r.join("phases/p1.json"), r#"{"title":"First phase","order":1}"#);
    write(&r.join("instruments/test.json"), r#"{"run":["true"],"answers":"Tests pass"}"#);
    write(&r.join("goals/first-goal.json"), r#"{"title":"First goal","done_when":"Tests pass","check":"test","broader":"p1"}"#);
    lab.cli(&root, &["check"]); lab.cli(&root, &["install-hooks"]);
    lab.git(&root, &["add", ".whosaidso"]); lab.git(&root, &["commit", "-q", "-m", "Add records"]);
    assert_eq!(lab.git(&root, &["status", "--porcelain"]), "", "control: the quick start leaves a clean tree");
    // README "Working with a team": start, work, land on main, then the lander prunes.
    lab.cli(&root, &["start", "first-goal"]);
    lab.git(&root, &["checkout", "-q", "-b", "work"]); commit_work(&lab, &root, "w.txt", "first-goal");
    lab.git(&root, &["checkout", "-q", "main"]); lab.git(&root, &["merge", "-q", "--no-ff", "work", "-m", "land"]);
    let status = lab.git(&root, &["status", "--porcelain"]);
    let o = lab.out(&root, BIN, &["prune"]);
    assert!(o.status.success(), "prune after a plain start/work/land flow was refused: {} (git status: {status:?})", String::from_utf8_lossy(&o.stderr));
}

// DEFECT. Expects: the worktree that wins a race for a goal can release it with `start none`, so origin holds no claim afterwards.
// Why it matters: the claim then stays on origin for 72 hours and refuses every other clone, although its holder already released it.
// Root cause: claims::claim writes refs/whosaidso/claims/<goal> with an unconditional update-ref before the lease push, and refs are shared by a clone's worktrees:
// the second worktree overwrites the winner's local ref, then deletes it when its push is rejected; claims::release only walks local claim refs.
#[test]
fn a_claim_won_in_a_race_between_two_worktrees_of_one_clone_can_still_be_released() {
    let lab = Lab::new("sameclone");
    let goals: Vec<String> = (0..12).map(|i| format!("g{i}")).collect();
    let names: Vec<&str> = goals.iter().map(String::as_str).collect();
    let t = lab.team(&names, &[]);
    let a = &t[0];
    let w = lab.home.join("a-second"); lab.git(a, &["worktree", "add", "-q", "-b", "second", w.to_str().unwrap(), "main"]);
    let origin = lab.home.join("origin.git");
    // Control: without a race the same worktree releases its claim on origin.
    lab.cli(&w, &["start", "g0"]); lab.cli(&w, &["start", "none"]);
    assert_eq!(lab.git(&origin, &["for-each-ref", "refs/whosaidso/claims"]), "", "control: sequential start then none frees origin");
    let mut leaked = Vec::new();
    for goal in goals.iter().skip(1) {
        let run = |dir: PathBuf| { let (home, goal) = (lab.home.clone(), goal.clone()); std::thread::spawn(move || command(&dir, &home, BIN, &["start", &goal]).output().unwrap()) };
        let (h1, h2) = (run(a.clone()), run(w.clone()));
        let (o1, o2) = (h1.join().unwrap(), h2.join().unwrap());
        let winner = match (o1.status.success(), o2.status.success()) { (true, false) => a, (false, true) => &w, other => panic!("exactly one worktree must win {goal}: {other:?} {o1:?} {o2:?}") };
        lab.cli(winner, &["start", "none"]);
        let left = lab.git(&origin, &["for-each-ref", &format!("refs/whosaidso/claims/{goal}")]);
        if !left.is_empty() { leaked.push(goal.clone()); lab.git(&origin, &["update-ref", "-d", &format!("refs/whosaidso/claims/{goal}")]); }
        for dir in [a, &w] { for r in lab.git(dir, &["for-each-ref", "--format=%(refname)", "refs/whosaidso"]).lines() { lab.git(dir, &["update-ref", "-d", r]); } }
    }
    assert!(leaked.is_empty(), "the winner ran `start none`, yet origin still holds its claim for {leaked:?}: the loser's cleanup or overwrite removed the local claim ref that `release` iterates, so the claim stays for 72 hours");
}

// ---- Coordinator addition: the viewer's proof ----

// Expects: a proof that is no longer eligible (here the instrument changed after it ran) is never the goal's current proof;
// the viewer marks the newest one as not current instead of showing "Latest proof: Pass".
#[test]
fn the_viewer_marks_an_ineligible_proof_as_not_current() {
    let lab = Lab::new("proofview");
    let root = with_branch(&lab, "proj");
    lab.cli(&root, &["prove", "g"]);
    let viewer = Viewer::start(&lab, &root);
    assert_eq!(viewer.goal("g")["proof"]["current"], json!(true), "control: the proof of the unchanged instrument decides the state");
    write(&root.join(".whosaidso/instruments/test.json"), json!({"run":["true"],"answers":"works, differently"}).to_string());
    lab.git(&root, &["add", "."]); lab.git(&root, &["commit", "-q", "-m", "change the instrument"]);
    let proof = viewer.goal("g")["proof"].clone();
    assert_eq!(proof["current"], json!(false), "an instrument change makes the old proof ineligible: {proof}");
    assert_eq!(proof["exit_code"], 0, "the newest proof is still offered, labelled");
    assert_eq!(viewer.goal("h")["proof"], Value::Null, "control: a goal never proved has none");
}

// The real viewer serves isolated repositories without changing their records.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{collections::BTreeMap, fs, io::{BufRead, BufReader, Read, Write}, net::TcpStream, path::{Path, PathBuf}, process::{Child, Command, Stdio}, time::{Duration, SystemTime, UNIX_EPOCH}};
fn command(root: &Path, home: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root).env("HOME", home).env("WHOSAIDSO_HOME", home.join("registry")).env("GIT_CONFIG_NOSYSTEM", "1").env("GIT_CEILING_DIRECTORIES", home.parent().unwrap()).env_remove("WHOSAIDSO_BLOBS").env_remove("WHOSAIDSO_ARCHIVE");
    if program == "git" { c.args(["-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"]); }
    c.args(args); c
}
fn ok(root: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let out = command(root, home, program, args).output().unwrap();
    assert!(out.status.success(), "{args:?}: {out:?}"); String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, home: &Path, args: &[&str]) -> String { ok(root, home, "git", args) }
fn cli(root: &Path, home: &Path, args: &[&str]) -> String { ok(root, home, env!("CARGO_BIN_EXE_whosaidso"), args) }
fn write(path: &Path, bytes: impl AsRef<[u8]>) { fs::create_dir_all(path.parent().unwrap()).unwrap(); fs::write(path, bytes).unwrap(); }
fn snapshot(root: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    let mut files = BTreeMap::new();
    for entry in fs::read_dir(root).unwrap() {
        let path = entry.unwrap().path();
        if path.is_dir() { files.extend(snapshot(&path)); } else { files.insert(path.clone(), fs::read(path).unwrap()); }
    }
    files
}
struct Viewer { child: Child, address: String, launch: String }
impl Viewer {
    fn start(root: &Path, home: &Path) -> Self {
        let mut child = command(root, home, env!("CARGO_BIN_EXE_whosaidso"), &["ui", "--port", "0"]).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().unwrap();
        let mut url = String::new(); BufReader::new(child.stdout.take().unwrap()).read_line(&mut url).unwrap();
        if !url.starts_with("http://127.0.0.1:") { let out = child.wait_with_output().unwrap(); panic!("viewer startup: {}", String::from_utf8_lossy(&out.stderr)); }
        let (address, launch) = url.trim().strip_prefix("http://").unwrap().split_once('/').unwrap();
        Self { child, address:address.into(), launch:format!("/{launch}") }
    }
    fn get(&self, path: &str, cookie: &str) -> (u16, String, Vec<u8>) { self.request("GET", path, cookie) }
    fn request(&self, method: &str, path: &str, cookie: &str) -> (u16, String, Vec<u8>) {
        let mut stream = TcpStream::connect(&self.address).unwrap(); stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
        write!(stream, "{method} {path} HTTP/1.1\r\nHost: {}\r\nCookie: {cookie}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", self.address).unwrap();
        let mut bytes = Vec::new(); stream.read_to_end(&mut bytes).unwrap();
        let split = bytes.windows(4).position(|b| b == b"\r\n\r\n").unwrap();
        let header = String::from_utf8(bytes[..split].to_vec()).unwrap(); let status = header.split_whitespace().nth(1).unwrap().parse().unwrap();
        (status, header, bytes[split+4..].to_vec())
    }
    fn cookie(&self) -> String {
        let (status, header, _) = self.get(&self.launch, ""); assert_eq!(status, 303);
        assert!(header.contains("HttpOnly") && header.contains("SameSite=Strict"));
        header.lines().find_map(|l| l.strip_prefix("Set-Cookie: ")).unwrap().split(';').next().unwrap().into()
    }
    fn json(&self, path: &str, cookie: &str) -> Value { let (status, _, bytes) = self.get(path, cookie); assert_eq!(status, 200, "{}", String::from_utf8_lossy(&bytes)); serde_json::from_slice(&bytes).unwrap() }
}
impl Drop for Viewer { fn drop(&mut self) { let _ = self.child.kill(); let _ = self.child.wait(); } }
#[test]
fn authenticated_read_only_screens_and_project_registry() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("ui-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    let root = home.join("project"); fs::create_dir_all(&root).unwrap(); git(&root, &home, &["init"]);
    let r = root.join(".whosaidso");
    for (id, order) in [("first", 1),("second", 2)] { write(&r.join(format!("phases/{id}.json")), json!({"title":id,"order":order}).to_string()); }
    for (id, check, phase) in [("done", "pass", "first"),("broken", "fail", "first"),("building", "pass", "second")] {
        write(&r.join(format!("goals/{id}.json")), json!({"title":format!("Goal {id}"),"done_when":"The check proves the goal","check":check,"broader":phase}).to_string());
    }
    write(&r.join("instruments/pass.json"), json!({"run":["true"],"answers":"passes"}).to_string());
    write(&r.join("instruments/fail.json"), json!({"run":["sh","-c","printf 'failure tail'; exit 7"],"answers":"reports failure"}).to_string());
    git(&root, &home, &["add", "."]); git(&root, &home, &["commit", "-m", "records"]);
    assert!(cli(&root, &home, &["prove", "done"]).contains("pass"));
    assert!(cli(&root, &home, &["prove", "broken"]).contains("fail"));
    assert!(cli(&root, &home, &["ask", "broken", "Why did the check fail?"]).contains("ask: broken"));
    let image = b"GIF89a\x01\0\x01\0\0\0\0"; let pdf = b"%PDF-1.7\n%%EOF\n";
    write(&home.join("image.gif"), image); write(&home.join("report.pdf"), pdf);
    cli(&root, &home, &["media", "broken", home.join("image.gif").to_str().unwrap(), "An image <evidence>"]);
    cli(&root, &home, &["media", "done", home.join("report.pdf").to_str().unwrap(), "PDF report"]);
    let archived = home.join("archived"); fs::create_dir(&archived).unwrap();
    let record = fs::read_dir(r.join("media")).unwrap().map(|e| e.unwrap().path()).find(|p| serde_json::from_slice::<Value>(&fs::read(p).unwrap()).unwrap()["ext"] == "pdf").unwrap();
    let mut value: Value = serde_json::from_slice(&fs::read(record).unwrap()).unwrap(); value["ran_at"] = json!(1767225600000000000i64);
    let bytes = serde_json::to_vec(&value).unwrap(); let id: String = Sha256::digest(&bytes).iter().map(|b| format!("{b:02x}")).collect();
    write(&archived.join(format!("{id}.json")), bytes); write(&archived.join(format!("{id}.pdf")), pdf);
    let bundle = root.join(".git/whosaidso/archive/media-2026-01.tar"); fs::create_dir_all(bundle.parent().unwrap()).unwrap();
    ok(&root, &home, "tar", &["--format=ustar", "-cf", bundle.to_str().unwrap(), "-C", archived.to_str().unwrap(), "."]);
    git(&root, &home, &["add", "."]); git(&root, &home, &["commit", "-m", "evidence"]);
    git(&root, &home, &["checkout", "-b", "builder"]); write(&root.join("code"), "work");
    git(&root, &home, &["add", "code"]); git(&root, &home, &["commit", "-m", "work", "-m", "Goal: building"]); git(&root, &home, &["checkout", "main"]);
    let unknown: String = Sha256::digest(b"unrecorded bytes").iter().map(|b| format!("{b:02x}")).collect();
    write(&root.join(format!(".git/whosaidso/blobs/{unknown}.gif")), b"unrecorded bytes");
    let before = git(&root, &home, &["status", "--porcelain"]); let files = snapshot(&r);
    let refused = command(&root, &home, env!("CARGO_BIN_EXE_whosaidso"), &["ui"]).env("WHOSAIDSO_HOME", root.join("viewer-home")).output().unwrap();
    assert_eq!(refused.status.code(), Some(1)); assert!(String::from_utf8_lossy(&refused.stderr).contains("registry must be outside")); assert!(!root.join("viewer-home").exists());
    let viewer = Viewer::start(&root, &home);
    assert_eq!(viewer.launch.split("token=").nth(1).unwrap().len(), 32);
    for path in ["/", "/ui.js", "/api/overview", "/?token=wrong"] { assert_eq!(viewer.get(path, "").0, 403); }
    let cookie = viewer.cookie();
    assert_eq!(viewer.get(&viewer.launch, "").0, 403, "the launch token is single-use");
    assert_eq!(viewer.get("/api/overview", "whosaidso=wrong").0, 403);
    let html = String::from_utf8(viewer.get("/", &cookie).2).unwrap();
    assert!(html.contains("WhoSaidSo") && html.contains("Activity") && !html.contains("favicon"));
    for asset in ["ui.css","ui-screens.css","ui-gallery.css","ui.js","ui-shell.js","ui-browser.js","ui-overview.js","ui-detail.js","ui-gallery.js","ui-gallery-media.js","ui-gallery-actions.js","ui-gallery-inspect.js","ui-components.js","ui-icons.js","ui-parts.js","ui-activity.js"] { assert_eq!(viewer.get(&format!("/{asset}"), &cookie).0, 200); }
    let overview = viewer.json("/api/overview", &cookie);
    let goals = viewer.json("/api/goals", &cookie); let cli_goals = cli(&root, &home, &["goals"]);
    for (name, count) in overview["counts"].as_object().unwrap() {
        let actual = cli_goals.lines().filter(|line| line.split_whitespace().next() == Some(name)).count(); assert_eq!(count.as_u64().unwrap(), actual as u64);
    }
    assert_eq!(overview["phases"][0]["done"], 1); assert_eq!(overview["phases"][0]["total"], 2);
    assert_eq!(overview["in_flight"][0]["active_branches"], json!(["builder"])); assert_eq!(goals["goals"].as_array().unwrap().len(), 3);
    let detail = viewer.json("/api/goal?id=broken", &cookie);
    assert_eq!(detail["newest_proof"]["exit_code"], 7); assert_eq!(detail["proof"]["exit_code"], 7);
    assert_eq!(detail["newest_proof"]["output_tail"], "failure tail"); assert_eq!(detail["asks"][0]["said"], "Why did the check fail?");
    assert_eq!(detail["instrument"]["answers"], "reports failure");
    let gallery = viewer.json("/api/gallery", &cookie); assert_eq!(gallery["entries"].as_array().unwrap().len(), 2);
    assert_eq!(gallery["archives"][0]["month"], "2026-01"); assert_eq!(gallery["archives"][0]["count"], 1);
    assert_eq!(gallery["archives"][0]["path"], bundle.to_str().unwrap());
    for entry in gallery["entries"].as_array().unwrap() {
        let path = format!("/api/media/{}", entry["media_sha256"].as_str().unwrap()); let (status, header, bytes) = viewer.get(&path, &cookie);
        assert_eq!(status, 200); assert_eq!(bytes, if entry["ext"] == "gif" { image.as_slice() } else { pdf.as_slice() });
        assert!(header.contains(if entry["ext"] == "gif" { "image/gif" } else { "application/pdf" }));
        assert!(viewer.get(&(path + "?download=1"), &cookie).1.contains("Content-Disposition: attachment"));
    }
    assert_eq!(viewer.get(&format!("/api/media/{unknown}"), &cookie).0, 404);
    assert_eq!(viewer.request("POST", "/api/overview", &cookie).0, 405);
    let records = viewer.json("/api/records", &cookie); assert_eq!(records["records"].as_array().unwrap().len(), 10);
    assert_eq!(snapshot(&r), files, "viewing writes no files, including ignored cache files"); assert_eq!(git(&root, &home, &["status", "--porcelain"]), before);
    let registry = home.join("registry/projects"); let entry = format!("{}\n", root.display());
    assert_eq!(fs::read_to_string(&registry).unwrap(), entry, "ui registers the repository");
    drop(viewer);
    fs::write(&registry, "").unwrap(); cli(&root, &home, &["install-hooks"]);
    assert_eq!(fs::read_to_string(&registry).unwrap(), entry, "install-hooks independently registers the repository");
    let viewer = Viewer::start(&root, &home); let cookie = viewer.cookie();
    assert_eq!(fs::read_to_string(&registry).unwrap(), entry, "registration is idempotent");
    // Every registry entry is visible, but only repository roots with records can be selected.
    let empty = home.join("empty"); fs::create_dir(&empty).unwrap(); git(&empty, &home, &["init"]);
    let missing = home.join("missing"); let second = home.join("other-project"); fs::create_dir(&second).unwrap(); git(&second, &home, &["init"]);
    write(&second.join(".whosaidso/phases/other.json"), json!({"title":"Other project","order":1}).to_string());
    git(&second, &home, &["add", "."]); git(&second, &home, &["commit", "-m", "other records"]);
    fs::write(&registry, format!("{entry}{}\n{}\n{}\n", empty.display(), missing.display(), second.display())).unwrap();
    let projects = viewer.json("/api/projects", &cookie);
    assert_eq!(projects["projects"].as_array().unwrap().len(), 4); assert!(!projects["projects"][1]["available"].as_bool().unwrap()); assert!(projects["projects"][1]["reason"].as_str().unwrap().contains("whosaidso"));
    assert!(projects["projects"][2]["reason"].as_str().unwrap().contains("missing"));
    let selected = format!("?project={}", second.display());
    assert_eq!(viewer.json(&("/api/overview".to_owned()+&selected), &cookie)["phases"][0]["title"], "Other project");
    assert_eq!(viewer.json(&("/api/goals".to_owned()+&selected), &cookie)["goals"], json!([]));
    assert_eq!(viewer.json(&("/api/gallery".to_owned()+&selected), &cookie)["entries"], json!([]));
    assert_eq!(viewer.json(&("/api/records".to_owned()+&selected), &cookie)["records"].as_array().unwrap().len(), 1);
    assert_eq!(viewer.get(&format!("/api/overview?project={}", missing.display()), &cookie).0, 404);
    drop(viewer);
    let outside = Viewer::start(&home, &home); let cookie = outside.cookie();
    assert_eq!(outside.json("/api/overview", &cookie)["counts"], overview["counts"]);
    drop(outside); fs::remove_dir_all(home).unwrap();
}

#[test]
fn sample_game_engine_activity_branches_and_counts() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR")).join("target/e2e-tmp").join(format!("ui-sample-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
    fs::create_dir_all(&home).unwrap();
    let script = Path::new(env!("CARGO_MANIFEST_DIR")).join("scripts/sample-project.sh");
    let mut shape = None;
    for name in ["first", "second"] {
        let target = home.join(name);
        let out = command(&home, &home, "sh", &[script.to_str().unwrap(), target.to_str().unwrap()]).env("WHOSAIDSO", env!("CARGO_BIN_EXE_whosaidso")).output().unwrap();
        assert!(out.status.success(), "sample script: {out:?}");
        let root = target.join("game-engine");
        assert_eq!(String::from_utf8(out.stdout).unwrap().trim(), root.to_str().unwrap());
        assert_eq!(cli(&root, &home, &["check"]), "check: ok (3 phases, 9 goals, 2 instruments, 2 proofs)\n");
        assert_eq!(git(&root, &home, &["status", "--porcelain"]), "");
        let goals = cli(&root, &home, &["goals"]);
        let counts: BTreeMap<_, _> = ["done", "landed", "active", "idle", "failing", "open"].into_iter().map(|s|
            (s.to_owned(), goals.lines().filter(|l| l.split_whitespace().next() == Some(s)).count())).collect();
        assert_eq!(counts.values().sum::<usize>(), 9);
        assert!(counts.values().all(|&n| n > 0), "{goals}");
        if let Some(previous) = &shape { assert_eq!(previous, &counts); } else { shape = Some(counts); }
        let media: Vec<Value> = fs::read_dir(root.join(".whosaidso/media")).unwrap().map(|e| serde_json::from_slice(&fs::read(e.unwrap().path()).unwrap()).unwrap()).collect();
        assert_eq!(media.len(), 6);
        assert_eq!(media.iter().filter(|m| m["ext"] == "png").count(), 5);
        assert_eq!(media.iter().filter(|m| m["ext"] == "pdf").count(), 1);
        let dates = git(&root, &home, &["log", "--all", "--format=%an %at %ct"]);
        for author in ["Avery", "Jordan", "Sam"] { assert!(dates.lines().any(|l| l.starts_with(author))); }
        for line in dates.lines() { let fields: Vec<_> = line.split_whitespace().collect(); assert_eq!(fields[1], fields[2]); }
    }
    let root = home.join("first/game-engine");
    let before = snapshot(&root.join(".whosaidso"));
    let viewer = Viewer::start(&root, &home); let cookie = viewer.cookie();
    let project = format!("?project={}", root.display());
    let activity = viewer.json(&format!("/api/activity{project}"), &cookie);
    let entries = activity["entries"].as_array().unwrap();
    for kind in ["commit", "proof", "media", "ask"] { assert!(entries.iter().any(|e| e["kind"] == kind), "{activity}"); }
    assert!(entries.windows(2).all(|w| w[0]["time"].as_i64().unwrap() >= w[1]["time"].as_i64().unwrap()));
    let log = cli(&root, &home, &["log", "--limit", "500"]);
    let cli_order: Vec<_> = log.lines().map(|l| { let f: Vec<_> = l.split_whitespace().collect(); (f[2], f[3]) }).collect();
    assert_eq!(entries.iter().map(|e| (e["kind"].as_str().unwrap(), e["goal"].as_str().unwrap())).collect::<Vec<_>>(), cli_order);
    let gallery = viewer.json(&format!("/api/gallery{project}"), &cookie);
    for e in entries.iter().filter(|e| e["kind"] == "media") {
        assert!(gallery["entries"].as_array().unwrap().iter().any(|m| m["id"] == e["media_id"] && m["shows"] == e["title"]));
    }
    let goals = viewer.json(&format!("/api/goals{project}"), &cookie);
    let overview = viewer.json(&format!("/api/overview{project}"), &cookie);
    assert!(overview["screens"].as_array().unwrap().contains(&json!("activity")));
    for (name, count) in overview["counts"].as_object().unwrap() {
        let text = cli(&root, &home, &["goals"]);
        assert_eq!(count.as_u64().unwrap(), text.lines().filter(|l| l.split_whitespace().next() == Some(name)).count() as u64);
    }
    for (id, branch, author, worktree) in [("shadows", "render/shadows", "Avery", Some(home.join("first/shadow-work"))), ("broadphase", "physics/broadphase", "Jordan", Some(home.join("first/physics-work"))), ("prediction", "origin/net/prediction", "Sam", None)] {
        let goal = goals["goals"].as_array().unwrap().iter().find(|g| g["id"] == id).unwrap();
        assert_eq!(goal["branches"].as_array().unwrap().len(), 1);
        let b = &goal["branches"][0];
        assert_eq!(b["name"], branch); assert_eq!(b["author"], author); assert!(b["time"].as_i64().unwrap() > 0);
        assert_eq!(b["worktree"], json!(worktree));
        assert_eq!(viewer.json(&format!("/api/goal{project}&id={id}"), &cookie)["branches"], goal["branches"]);
        assert_eq!(overview["in_flight"].as_array().unwrap().iter().find(|g| g["id"] == id).unwrap()["branches"], goal["branches"]);
    }
    assert_eq!(snapshot(&root.join(".whosaidso")), before);
    // More than 500 valid entries make removing the API cap observable.
    let head = git(&root, &home, &["rev-parse", "HEAD"]);
    for n in 0..505 {
        let bytes = json!({"goal":"shadows","said":format!("sample request {n}"),"ran_at":n,"commit":head.trim()}).to_string();
        let hash: String = Sha256::digest(bytes.as_bytes()).iter().map(|b| format!("{b:02x}")).collect();
        write(&root.join(format!(".whosaidso/asks/{hash}.json")), bytes);
    }
    for (limit, count) in [("",100), ("&limit=3",3), ("&limit=501",500)] {
        assert_eq!(viewer.json(&format!("/api/activity{project}{limit}"), &cookie)["entries"].as_array().unwrap().len(), count);
    }
    for limit in ["0", "-1", "bad"] { assert_eq!(viewer.get(&format!("/api/activity{project}&limit={limit}"), &cookie).0, 400); }
    drop(viewer); fs::remove_dir_all(home).unwrap();
}

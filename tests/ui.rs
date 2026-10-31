// The real viewer serves isolated repositories without changing their records.
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs,
    io::{BufRead, BufReader, Read, Write},
    net::TcpStream,
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, SystemTime, UNIX_EPOCH},
};
fn command(root: &Path, home: &Path, program: &str, args: &[&str]) -> Command {
    let mut c = Command::new(program);
    c.current_dir(root)
        .env("HOME", home)
        .env("WHOSAIDSO_HOME", home.join("registry"))
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CEILING_DIRECTORIES", home.parent().unwrap())
        .env_remove("WHOSAIDSO_BLOBS")
        .env_remove("WHOSAIDSO_ARCHIVE");
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
    c.args(args);
    c
}
fn ok(root: &Path, home: &Path, program: &str, args: &[&str]) -> String {
    let out = command(root, home, program, args).output().unwrap();
    assert!(out.status.success(), "{args:?}: {out:?}");
    String::from_utf8(out.stdout).unwrap()
}
fn git(root: &Path, home: &Path, args: &[&str]) -> String {
    ok(root, home, "git", args)
}
fn cli(root: &Path, home: &Path, args: &[&str]) -> String {
    ok(root, home, env!("CARGO_BIN_EXE_whosaidso"), args)
}
fn write(path: &Path, bytes: impl AsRef<[u8]>) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, bytes).unwrap();
}
fn snapshot(root: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    let mut files = BTreeMap::new();
    for entry in fs::read_dir(root).unwrap() {
        let path = entry.unwrap().path();
        if path.is_dir() {
            files.extend(snapshot(&path));
        } else {
            files.insert(path.clone(), fs::read(path).unwrap());
        }
    }
    files
}
struct Viewer {
    child: Child,
    address: String,
    launch: String,
}
impl Viewer {
    fn start(root: &Path, home: &Path) -> Self {
        let mut child = command(
            root,
            home,
            env!("CARGO_BIN_EXE_whosaidso"),
            &["ui", "--port", "0"],
        )
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
        let mut url = String::new();
        BufReader::new(child.stdout.take().unwrap())
            .read_line(&mut url)
            .unwrap();
        if !url.starts_with("http://127.0.0.1:") {
            let out = child.wait_with_output().unwrap();
            panic!("viewer startup: {}", String::from_utf8_lossy(&out.stderr));
        }
        let (address, launch) = url
            .trim()
            .strip_prefix("http://")
            .unwrap()
            .split_once('/')
            .unwrap();
        Self {
            child,
            address: address.into(),
            launch: format!("/{launch}"),
        }
    }
    fn get(&self, path: &str, cookie: &str) -> (u16, String, Vec<u8>) {
        self.request("GET", path, cookie)
    }
    fn request(&self, method: &str, path: &str, cookie: &str) -> (u16, String, Vec<u8>) {
        let mut stream = TcpStream::connect(&self.address).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();
        write!(stream, "{method} {path} HTTP/1.1\r\nHost: {}\r\nCookie: {cookie}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", self.address).unwrap();
        let mut bytes = Vec::new();
        stream.read_to_end(&mut bytes).unwrap();
        let split = bytes.windows(4).position(|b| b == b"\r\n\r\n").unwrap();
        let header = String::from_utf8(bytes[..split].to_vec()).unwrap();
        let status = header.split_whitespace().nth(1).unwrap().parse().unwrap();
        (status, header, bytes[split + 4..].to_vec())
    }
    fn cookie(&self) -> String {
        let (status, header, _) = self.get(&self.launch, "");
        assert_eq!(status, 303);
        assert!(header.contains("HttpOnly") && header.contains("SameSite=Strict"));
        header
            .lines()
            .find_map(|l| l.strip_prefix("Set-Cookie: "))
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .into()
    }
    fn json(&self, path: &str, cookie: &str) -> Value {
        let (status, _, bytes) = self.get(path, cookie);
        assert_eq!(status, 200, "{}", String::from_utf8_lossy(&bytes));
        serde_json::from_slice(&bytes).unwrap()
    }
}
impl Drop for Viewer {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
#[test]
fn two_screens_authenticated_read_only_and_project_registry() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("target/e2e-tmp")
        .join(format!(
            "ui-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
    let root = home.join("project");
    fs::create_dir_all(&root).unwrap();
    git(&root, &home, &["init"]);
    let r = root.join(".whosaidso");
    for (id, order) in [("first", 1), ("second", 2)] {
        write(
            &r.join(format!("phases/{id}.json")),
            json!({"title":id,"order":order}).to_string(),
        );
    }
    for (id, check, phase) in [
        ("done", "pass", "first"),
        ("broken", "fail", "first"),
        ("building", "pass", "second"),
    ] {
        write(&r.join(format!("goals/{id}.json")), json!({"title":format!("Goal {id}"),"done_when":"The check proves the goal","check":check,"broader":phase}).to_string());
    }
    write(
        &r.join("instruments/pass.json"),
        json!({"run":["true"],"answers":"passes"}).to_string(),
    );
    write(
        &r.join("instruments/fail.json"),
        json!({"run":["sh","-c","printf 'failure tail'; exit 7"],"answers":"reports failure"})
            .to_string(),
    );
    git(&root, &home, &["add", "."]);
    git(&root, &home, &["commit", "-m", "records"]);
    assert!(cli(&root, &home, &["prove", "done"]).contains("pass"));
    assert!(cli(&root, &home, &["prove", "broken"]).contains("fail"));
    assert!(
        cli(&root, &home, &["ask", "broken", "Why did the check fail?"]).contains("ask: broken")
    );
    let image = b"GIF89a\x01\0\x01\0\0\0\0";
    let pdf = b"%PDF-1.7\n%%EOF\n";
    write(&home.join("image.gif"), image);
    write(&home.join("report.pdf"), pdf);
    cli(
        &root,
        &home,
        &[
            "media",
            "broken",
            home.join("image.gif").to_str().unwrap(),
            "An image <evidence>",
        ],
    );
    cli(
        &root,
        &home,
        &[
            "media",
            "done",
            home.join("report.pdf").to_str().unwrap(),
            "PDF report",
        ],
    );
    let archived = home.join("archived");
    fs::create_dir(&archived).unwrap();
    let record = fs::read_dir(r.join("media"))
        .unwrap()
        .map(|e| e.unwrap().path())
        .find(|p| serde_json::from_slice::<Value>(&fs::read(p).unwrap()).unwrap()["ext"] == "pdf")
        .unwrap();
    let mut value: Value = serde_json::from_slice(&fs::read(record).unwrap()).unwrap();
    value["ran_at"] = json!(1767225600000000000i64);
    let bytes = serde_json::to_vec(&value).unwrap();
    let id: String = Sha256::digest(&bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    write(&archived.join(format!("{id}.json")), bytes);
    write(&archived.join(format!("{id}.pdf")), pdf);
    let bundle = root.join(".git/whosaidso/archive/media-2026-01.tar");
    fs::create_dir_all(bundle.parent().unwrap()).unwrap();
    ok(
        &root,
        &home,
        "tar",
        &[
            "--format=ustar",
            "-cf",
            bundle.to_str().unwrap(),
            "-C",
            archived.to_str().unwrap(),
            ".",
        ],
    );
    git(&root, &home, &["add", "."]);
    git(&root, &home, &["commit", "-m", "evidence"]);
    git(&root, &home, &["checkout", "-b", "builder"]);
    write(&root.join("code"), "work");
    git(&root, &home, &["add", "code"]);
    git(
        &root,
        &home,
        &["commit", "-m", "work", "-m", "Goal: building"],
    );
    git(&root, &home, &["checkout", "main"]);
    let unknown: String = Sha256::digest(b"unrecorded bytes")
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    write(
        &root.join(format!(".git/whosaidso/blobs/{unknown}.gif")),
        b"unrecorded bytes",
    );
    let before = git(&root, &home, &["status", "--porcelain"]);
    let files = snapshot(&r);
    let refused = command(&root, &home, env!("CARGO_BIN_EXE_whosaidso"), &["ui"])
        .env("WHOSAIDSO_HOME", root.join("viewer-home"))
        .output()
        .unwrap();
    assert_eq!(refused.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&refused.stderr).contains("registry must be outside"));
    assert!(!root.join("viewer-home").exists());
    let viewer = Viewer::start(&root, &home);
    assert_eq!(viewer.launch.split("token=").nth(1).unwrap().len(), 32);
    for path in ["/", "/ui.js", "/api/goals", "/?token=wrong"] {
        assert_eq!(viewer.get(path, "").0, 403);
    }
    let cookie = viewer.cookie();
    assert_eq!(
        viewer.get(&viewer.launch, "").0,
        403,
        "the launch token is single-use"
    );
    assert_eq!(viewer.get("/api/goals", "whosaidso=wrong").0, 403);
    let html = String::from_utf8(viewer.get("/", &cookie).2).unwrap();
    // Exactly two screens, and no title or wordmark on the page itself (the tab title stays).
    assert_eq!(html.matches("data-nav=").count(), 2);
    assert!(html.contains("data-nav=\"goals\"") && html.contains("data-nav=\"gallery\""));
    for gone in ["Overview", "Records", "Activity", "wordmark"] {
        assert!(!html.contains(gone), "{gone}");
    }
    assert_eq!(
        html.replace("<title>WhoSaidSo</title>", "")
            .matches("WhoSaidSo")
            .count(),
        0
    );
    let mut shipped = html.to_lowercase();
    for asset in [
        "ui.css",
        "ui-gallery.css",
        "ui.js",
        "ui-shell.js",
        "ui-goals.js",
        "ui-gallery.js",
        "ui-gallery-media.js",
        "ui-gallery-actions.js",
        "ui-gallery-inspect.js",
        "ui-components.js",
        "ui-icons.js",
        "ui-parts.js",
    ] {
        let (status, _, bytes) = viewer.get(&format!("/{asset}"), &cookie);
        assert_eq!(status, 200, "{asset}");
        shipped += &String::from_utf8(bytes).unwrap().to_lowercase();
    }
    assert!(
        !shipped.contains("refresh"),
        "the viewer updates itself and offers no refresh control"
    );
    assert!(!shipped.contains("gaps"), "instrument gaps are not shown");
    for asset in [
        "ui-screens.css",
        "ui-overview.js",
        "ui-browser.js",
        "ui-detail.js",
        "ui-activity.js",
    ] {
        assert_eq!(viewer.get(&format!("/{asset}"), &cookie).0, 404, "{asset}");
    }
    for path in [
        "/api/overview",
        "/api/activity",
        "/api/records",
        "/api/record?id=done",
        "/api/goal?id=done",
    ] {
        assert_eq!(viewer.get(path, &cookie).0, 404, "{path}");
    }
    let goals = viewer.json("/api/goals", &cookie);
    let cli_goals = cli(&root, &home, &["goals"]);
    for (name, count) in goals["totals"].as_object().unwrap() {
        let actual = cli_goals
            .lines()
            .filter(|line| line.split_whitespace().next() == Some(name))
            .count();
        assert_eq!(count.as_u64().unwrap(), actual as u64, "{name}");
    }
    assert_eq!(goals["phases"][0]["id"], "first");
    assert_eq!(goals["phases"][0]["counts"], json!({"done":1,"failing":1}));
    assert_eq!(goals["goals"].as_array().unwrap().len(), 3);
    let goal = |id: &str| {
        goals["goals"]
            .as_array()
            .unwrap()
            .iter()
            .find(|g| g["id"] == id)
            .unwrap()
            .clone()
    };
    assert_eq!(goal("building")["branches"][0]["name"], "builder");
    assert_eq!(goal("building")["state"], "active");
    let broken = goal("broken");
    assert_eq!(broken["proof"]["exit_code"], 7);
    assert_eq!(broken["proof"]["commit"].as_str().unwrap().len(), 40);
    assert_eq!(broken["proof"]["output_tail"], "failure tail");
    assert_eq!(broken["asks"][0]["said"], "Why did the check fail?");
    assert_eq!(broken["instrument"]["answers"], "reports failure");
    assert_eq!(broken["done_when"], "The check proves the goal");
    assert_eq!(broken["media"][0]["shows"], "An image <evidence>");
    assert_eq!(broken["media"][0]["tag"], "img");
    assert!(broken["instrument"].get("gaps").is_none());
    assert_eq!(broken["claim"], Value::Null);
    assert_eq!(
        goals["version"],
        viewer.json("/api/version", &cookie)["version"],
        "the token a screen carries is the one the poll compares"
    );
    let gallery = viewer.json("/api/gallery", &cookie);
    assert_eq!(gallery["entries"].as_array().unwrap().len(), 2);
    assert_eq!(gallery["archives"][0]["month"], "2026-01");
    assert_eq!(gallery["archives"][0]["count"], 1);
    assert_eq!(gallery["archives"][0]["path"], bundle.to_str().unwrap());
    for entry in gallery["entries"].as_array().unwrap() {
        let path = format!("/api/media/{}", entry["media_sha256"].as_str().unwrap());
        let (status, header, bytes) = viewer.get(&path, &cookie);
        assert_eq!(status, 200);
        assert_eq!(
            bytes,
            if entry["ext"] == "gif" {
                image.as_slice()
            } else {
                pdf.as_slice()
            }
        );
        assert!(header.contains(if entry["ext"] == "gif" {
            "image/gif"
        } else {
            "application/pdf"
        }));
        assert!(
            viewer
                .get(&(path + "?download=1"), &cookie)
                .1
                .contains("Content-Disposition: attachment")
        );
    }
    assert_eq!(viewer.get(&format!("/api/media/{unknown}"), &cookie).0, 404);
    assert_eq!(viewer.request("POST", "/api/goals", &cookie).0, 405);
    assert_eq!(
        snapshot(&r),
        files,
        "viewing writes no files, including ignored cache files"
    );
    assert_eq!(git(&root, &home, &["status", "--porcelain"]), before);
    let registry = home.join("registry/projects");
    let entry = format!("{}\n", root.display());
    assert_eq!(
        fs::read_to_string(&registry).unwrap(),
        entry,
        "ui registers the repository"
    );
    drop(viewer);
    fs::write(&registry, "").unwrap();
    cli(&root, &home, &["install-hooks"]);
    assert_eq!(
        fs::read_to_string(&registry).unwrap(),
        entry,
        "install-hooks independently registers the repository"
    );
    let viewer = Viewer::start(&root, &home);
    let cookie = viewer.cookie();
    assert_eq!(
        fs::read_to_string(&registry).unwrap(),
        entry,
        "registration is idempotent"
    );
    // Every registry entry is visible, but only repository roots with records can be selected.
    let empty = home.join("empty");
    fs::create_dir(&empty).unwrap();
    git(&empty, &home, &["init"]);
    let missing = home.join("missing");
    let second = home.join("other-project");
    fs::create_dir(&second).unwrap();
    git(&second, &home, &["init"]);
    write(
        &second.join(".whosaidso/phases/other.json"),
        json!({"title":"Other project","order":1}).to_string(),
    );
    git(&second, &home, &["add", "."]);
    git(&second, &home, &["commit", "-m", "other records"]);
    fs::write(
        &registry,
        format!(
            "{entry}{}\n{}\n{}\n",
            empty.display(),
            missing.display(),
            second.display()
        ),
    )
    .unwrap();
    let projects = viewer.json("/api/projects", &cookie);
    assert_eq!(projects["projects"].as_array().unwrap().len(), 4);
    assert!(!projects["projects"][1]["available"].as_bool().unwrap());
    assert!(
        projects["projects"][1]["reason"]
            .as_str()
            .unwrap()
            .contains("whosaidso")
    );
    assert!(
        projects["projects"][2]["reason"]
            .as_str()
            .unwrap()
            .contains("missing")
    );
    let selected = format!("?project={}", second.display());
    assert_eq!(
        viewer.json(&("/api/goals".to_owned() + &selected), &cookie)["phases"][0]["title"],
        "Other project"
    );
    assert_eq!(
        viewer.json(&("/api/goals".to_owned() + &selected), &cookie)["goals"],
        json!([])
    );
    assert_eq!(
        viewer.json(&("/api/gallery".to_owned() + &selected), &cookie)["entries"],
        json!([])
    );
    assert_ne!(
        viewer.json(&("/api/version".to_owned() + &selected), &cookie)["version"],
        viewer.json("/api/version", &cookie)["version"]
    );
    assert_eq!(
        viewer
            .get(
                &format!("/api/goals?project={}", missing.display()),
                &cookie
            )
            .0,
        404
    );
    drop(viewer);
    let outside = Viewer::start(&home, &home);
    let cookie = outside.cookie();
    assert_eq!(
        outside.json("/api/goals", &cookie)["totals"],
        goals["totals"]
    );
    drop(outside);
    fs::remove_dir_all(home).unwrap();
}

#[test]
fn sample_game_engine_work_claims_and_auto_refresh() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("target/e2e-tmp")
        .join(format!(
            "ui-sample-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
    fs::create_dir_all(&home).unwrap();
    let script = Path::new(env!("CARGO_MANIFEST_DIR")).join("scripts/sample-project.sh");
    let mut shape = None;
    for name in ["first", "second"] {
        let target = home.join(name);
        let out = command(
            &home,
            &home,
            "sh",
            &[script.to_str().unwrap(), target.to_str().unwrap()],
        )
        .env("WHOSAIDSO", env!("CARGO_BIN_EXE_whosaidso"))
        .output()
        .unwrap();
        assert!(out.status.success(), "sample script: {out:?}");
        let root = target.join("game-engine");
        assert_eq!(
            String::from_utf8(out.stdout).unwrap().trim(),
            root.to_str().unwrap()
        );
        assert_eq!(
            cli(&root, &home, &["check"]),
            "check: ok (3 phases, 9 goals, 2 instruments, 2 proofs)\n"
        );
        assert_eq!(git(&root, &home, &["status", "--porcelain"]), "");
        let goals = cli(&root, &home, &["goals"]);
        let counts: BTreeMap<_, _> = ["done", "landed", "active", "idle", "failing", "open"]
            .into_iter()
            .map(|s| {
                (
                    s.to_owned(),
                    goals
                        .lines()
                        .filter(|l| l.split_whitespace().next() == Some(s))
                        .count(),
                )
            })
            .collect();
        assert_eq!(counts.values().sum::<usize>(), 9);
        assert!(counts.values().all(|&n| n > 0), "{goals}");
        if let Some(previous) = &shape {
            assert_eq!(previous, &counts);
        } else {
            shape = Some(counts);
        }
        let media: Vec<Value> = fs::read_dir(root.join(".whosaidso/media"))
            .unwrap()
            .map(|e| serde_json::from_slice(&fs::read(e.unwrap().path()).unwrap()).unwrap())
            .collect();
        assert_eq!(media.len(), 6);
        assert_eq!(media.iter().filter(|m| m["ext"] == "png").count(), 5);
        assert_eq!(media.iter().filter(|m| m["ext"] == "pdf").count(), 1);
        let dates = git(&root, &home, &["log", "--all", "--format=%an %at %ct"]);
        for author in ["Avery", "Jordan", "Sam"] {
            assert!(dates.lines().any(|l| l.starts_with(author)));
        }
        for line in dates.lines() {
            let fields: Vec<_> = line.split_whitespace().collect();
            assert_eq!(fields[1], fields[2]);
        }
    }
    let root = home.join("first/game-engine");
    let before = snapshot(&root.join(".whosaidso"));
    let viewer = Viewer::start(&root, &home);
    let cookie = viewer.cookie();
    let project = format!("?project={}", root.display());
    let goals = viewer.json(&format!("/api/goals{project}"), &cookie);
    let text = cli(&root, &home, &["goals"]);
    for (name, count) in goals["totals"].as_object().unwrap() {
        assert_eq!(
            count.as_u64().unwrap(),
            text.lines()
                .filter(|l| l.split_whitespace().next() == Some(name))
                .count() as u64,
            "{name}"
        );
    }
    assert_eq!(
        goals["phases"]
            .as_array()
            .unwrap()
            .iter()
            .map(|p| p["counts"]
                .as_object()
                .unwrap()
                .values()
                .map(|n| n.as_u64().unwrap())
                .sum::<u64>())
            .sum::<u64>(),
        9
    );
    // Where work happens: branch, author, worktree, and the claim holder from the claim refs (a local ref, or one published by another clone).
    for (id, branch, author, worktree, claim) in [
        (
            "shadows",
            "render/shadows",
            "Avery",
            Some(home.join("first/shadow-work")),
            "render/shadows",
        ),
        (
            "broadphase",
            "physics/broadphase",
            "Jordan",
            Some(home.join("first/physics-work")),
            "physics/broadphase",
        ),
        (
            "prediction",
            "origin/net/prediction",
            "Sam",
            None,
            "net/prediction",
        ),
        ("lighting", "", "", None, ""),
    ] {
        let goal = goals["goals"]
            .as_array()
            .unwrap()
            .iter()
            .find(|g| g["id"] == id)
            .unwrap();
        if branch.is_empty() {
            assert_eq!(goal["branches"], json!([]));
            assert_eq!(goal["claim"], Value::Null);
            continue;
        }
        assert_eq!(goal["branches"].as_array().unwrap().len(), 1);
        let b = &goal["branches"][0];
        assert_eq!(b["name"], branch);
        assert_eq!(b["author"], author);
        assert!(b["time"].as_i64().unwrap() > 0);
        assert_eq!(b["worktree"], json!(worktree));
        let c = &goal["claim"];
        assert_eq!(c["branch"], claim);
        assert_eq!(c["author"], author);
        assert!(c["since"].as_i64().unwrap() > 0);
        assert!(c["path"].as_str().unwrap().ends_with(match id {
            "shadows" => "shadow-work",
            "broadphase" => "physics-work",
            _ => "remote-engine",
        }));
    }
    assert_eq!(snapshot(&root.join(".whosaidso")), before);
    // The viewer follows the project by itself: the token holds still while nothing changes, then moves for a new record and again for the commit that lands it.
    let version = || {
        viewer.json(&format!("/api/version{project}"), &cookie)["version"]
            .as_str()
            .unwrap()
            .to_owned()
    };
    let (first, asks) = (version(), |g: &Value| g["asks"].as_array().unwrap().len());
    assert_eq!(version(), first);
    assert_eq!(goals["version"], first.as_str());
    cli(
        &root,
        &home,
        &["ask", "lighting", "Add a second light pass"],
    );
    let recorded = version();
    assert_ne!(recorded, first);
    let after = viewer.json(&format!("/api/goals{project}"), &cookie);
    assert_eq!(after["version"], recorded.as_str());
    let lighting = after["goals"]
        .as_array()
        .unwrap()
        .iter()
        .find(|g| g["id"] == "lighting")
        .unwrap()
        .clone();
    assert_eq!(asks(&lighting), 1);
    assert_eq!(lighting["asks"][0]["said"], "Add a second light pass");
    git(&root, &home, &["add", ".whosaidso"]);
    git(&root, &home, &["commit", "-q", "-m", "Record a request"]);
    let committed = version();
    assert_ne!(committed, recorded);
    git(&root, &home, &["branch", "extra"]);
    assert_ne!(version(), committed, "a new branch moves the token");
    drop(viewer);
    fs::remove_dir_all(home).unwrap();
}
#[test]
fn a_running_viewer_sees_a_branch_go_idle() {
    let home = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("target/e2e-tmp")
        .join(format!(
            "idle-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
    let root = home.join("project");
    fs::create_dir_all(&root).unwrap();
    git(&root, &home, &["init"]);
    let r = root.join(".whosaidso");
    write(
        &r.join("phases/one.json"),
        json!({"title":"One","order":1}).to_string(),
    );
    write(
        &r.join("goals/g.json"),
        json!({"title":"Goal","done_when":"done","check":"pass","broader":"one"}).to_string(),
    );
    write(
        &r.join("instruments/pass.json"),
        json!({"run":["true"],"answers":"passes"}).to_string(),
    );
    git(&root, &home, &["add", "."]);
    git(&root, &home, &["commit", "-m", "records"]);
    // The branch's last commit is six seconds short of the 72-hour mark when the viewer starts.
    let then = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs()
        - 72 * 3600
        + 6;
    git(&root, &home, &["checkout", "-b", "work"]);
    write(&root.join("code"), "work");
    git(&root, &home, &["add", "code"]);
    let out = command(
        &root,
        &home,
        "git",
        &["commit", "-m", "work", "-m", "Goal: g"],
    )
    .env("GIT_COMMITTER_DATE", format!("{then} +0000"))
    .output()
    .unwrap();
    assert!(out.status.success(), "{out:?}");
    git(&root, &home, &["checkout", "main"]);
    let viewer = Viewer::start(&root, &home);
    let cookie = viewer.cookie();
    let state = || {
        viewer.json("/api/goals", &cookie)["goals"][0]["state"]
            .as_str()
            .unwrap()
            .to_owned()
    };
    let version = viewer.json("/api/version", &cookie)["version"]
        .as_str()
        .unwrap()
        .to_owned();
    assert_eq!(state(), "active");
    std::thread::sleep(Duration::from_secs(8));
    assert_eq!(state(), "idle", "the viewer's clock keeps moving");
    assert_ne!(
        viewer.json("/api/version", &cookie)["version"]
            .as_str()
            .unwrap(),
        version,
        "the poll sees the lapse, so open pages refresh"
    );
    drop(viewer);
    fs::remove_dir_all(home).unwrap();
}

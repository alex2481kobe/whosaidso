// Several machines sharing one bare remote. Each machine has its own full clone, HOME, WHOSAIDSO_HOME,
// git identity and caches, so the remote is the only thing they share. Only the real binary is driven.
#![allow(dead_code)]
mod common;
use common::process::tool_path;
use common::{BIN, hash, png};
use serde_json::{Value, json};
use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output},
    sync::{
        Barrier,
        atomic::{AtomicU64, Ordering},
    },
    time::{SystemTime, UNIX_EPOCH},
};

static NEXT: AtomicU64 = AtomicU64::new(0);

pub struct World {
    dir: PathBuf,
    origin: PathBuf,
    tested: PathBuf,
}

pub struct Machine {
    name: String,
    home: PathBuf,
    repo: PathBuf,
    env: Vec<(String, String)>,
}

fn text(out: &Output) -> String {
    format!(
        "{}{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    )
}

impl World {
    fn new() -> World {
        // The cargo-provided scratch directory sits under the target directory, never under /tmp.
        let dir = Path::new(env!("CARGO_TARGET_TMPDIR")).join(format!(
            "multi-machine-{}-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        let origin = dir.join("origin.git");
        fs::create_dir_all(&dir).unwrap();
        let tested = dir.join("tested.log");
        fs::write(&tested, "").unwrap();
        let world = World {
            dir,
            origin,
            tested,
        };
        let out = Command::new("git")
            .env("PATH", tool_path())
            .args(["init", "--bare", "-q", "-b", "main"])
            .arg(&world.origin)
            .output()
            .unwrap();
        assert!(out.status.success(), "{}", text(&out));
        world
    }

    fn machine(&self, name: &str) -> Machine {
        let home = self.dir.join(name).join("home");
        fs::create_dir_all(home.join("tmp")).unwrap();
        fs::write(
            home.join(".gitconfig"),
            format!(
                "[user]\n\tname = {name}\n\temail = {name}@machine.example\n[init]\n\tdefaultBranch = main\n[advice]\n\tdetachedHead = false\n"
            ),
        )
        .unwrap();
        Machine {
            name: name.into(),
            repo: self.dir.join(name).join("repo"),
            home,
            env: vec![("TESTED".into(), self.tested.display().to_string())],
        }
    }

    // A machine that has never seen the project before: a full clone of the remote.
    fn clone_machine(&self, name: &str) -> Machine {
        let m = self.machine(name);
        let out = m
            .cmd(
                "git",
                &[
                    "clone",
                    "-q",
                    "--no-local",
                    self.origin.to_str().unwrap(),
                    m.repo.to_str().unwrap(),
                ],
                &m.home,
            )
            .output()
            .unwrap();
        assert!(out.status.success(), "{}", text(&out));
        // Local main only ever follows the remote, so no worktree keeps it checked out.
        m.git(&["switch", "-q", "-c", "idle"]);
        m
    }

    // The first machine creates the project and publishes it.
    fn first_machine(&self, name: &str) -> Machine {
        let m = self.machine(name);
        fs::create_dir_all(&m.repo).unwrap();
        m.git(&["init", "-q"]);
        m.git(&["remote", "add", "origin", self.origin.to_str().unwrap()]);
        m
    }

    fn origin_git(&self, args: &[&str]) -> String {
        let out = Command::new("git")
            .env("PATH", tool_path())
            .env("GIT_CONFIG_GLOBAL", "/dev/null")
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .arg("--git-dir")
            .arg(&self.origin)
            .args(args)
            .output()
            .unwrap();
        assert!(out.status.success(), "origin git {args:?}: {}", text(&out));
        String::from_utf8(out.stdout).unwrap()
    }
    // Every claim ref on the remote, goal -> claim text.
    fn claims(&self) -> Vec<(String, String)> {
        self.origin_git(&[
            "for-each-ref",
            "--format=%(refname) %(objectname)",
            "refs/whosaidso/claims/",
        ])
        .lines()
        .map(|l| {
            let (r, oid) = l.split_once(' ').unwrap();
            (
                r.trim_start_matches("refs/whosaidso/claims/").to_owned(),
                self.origin_git(&["cat-file", "blob", oid]),
            )
        })
        .collect()
    }
    fn claim(&self, goal: &str) -> Option<String> {
        self.claims()
            .into_iter()
            .find(|(g, _)| g == goal)
            .map(|c| c.1)
    }

    // The remote accepts only what the real tool published: a receipt trailer whose candidate was tested.
    fn tested_candidates(&self) -> Vec<String> {
        fs::read_to_string(&self.tested)
            .unwrap()
            .lines()
            .map(str::to_owned)
            .collect()
    }
}

impl Machine {
    fn cmd(&self, program: &str, args: &[&str], cwd: &Path) -> Command {
        let mut c = Command::new(program);
        c.current_dir(cwd)
            .env_clear()
            .env("PATH", tool_path())
            .env("HOME", &self.home)
            .env("TMPDIR", self.home.join("tmp"))
            .env("XDG_CACHE_HOME", self.home.join(".cache"))
            .env("XDG_CONFIG_HOME", self.home.join(".config"))
            .env("WHOSAIDSO_HOME", self.home.join(".whosaidso"))
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .env("GIT_CONFIG_GLOBAL", self.home.join(".gitconfig"))
            .env("GIT_AUTHOR_NAME", &self.name)
            .env("GIT_AUTHOR_EMAIL", format!("{}@machine.example", self.name))
            .env("GIT_COMMITTER_NAME", &self.name)
            .env(
                "GIT_COMMITTER_EMAIL",
                format!("{}@machine.example", self.name),
            )
            .args(args);
        for (k, v) in &self.env {
            c.env(k, v);
        }
        c
    }
    fn git_out(&self, args: &[&str]) -> Output {
        self.cmd("git", args, &self.repo).output().unwrap()
    }
    fn git(&self, args: &[&str]) -> String {
        let out = self.git_out(args);
        assert!(
            out.status.success(),
            "{}: git {args:?} failed: {}",
            self.name,
            text(&out)
        );
        String::from_utf8(out.stdout).unwrap()
    }
    fn ws_cmd(&self, args: &[&str]) -> Command {
        self.cmd(BIN, args, &self.repo)
    }
    fn ws(&self, args: &[&str]) -> Output {
        self.ws_cmd(args).output().unwrap()
    }
    // The binary must succeed; returns everything it printed.
    fn ok(&self, args: &[&str]) -> String {
        let out = self.ws(args);
        assert!(
            out.status.success(),
            "{}: whosaidso {args:?} failed: {}",
            self.name,
            text(&out)
        );
        text(&out)
    }
    // The binary must refuse with status 1 and say `expected`.
    fn refuse(&self, args: &[&str], expected: &str) -> String {
        let out = self.ws(args);
        let said = text(&out);
        assert_eq!(
            out.status.code(),
            Some(1),
            "{}: whosaidso {args:?} should have been refused: {said}",
            self.name
        );
        assert!(
            said.contains(expected),
            "{}: whosaidso {args:?} refused without saying {expected:?}: {said}",
            self.name
        );
        said
    }
    fn write(&self, rel: &str, bytes: impl AsRef<[u8]>) {
        let path = self.repo.join(rel);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, bytes).unwrap();
    }
    fn read(&self, rel: &str) -> String {
        fs::read_to_string(self.repo.join(rel)).unwrap()
    }
    fn exists(&self, rel: &str) -> bool {
        self.repo.join(rel).exists()
    }
    fn commit_all(&self, message: &str, goal: &str) {
        self.git(&["add", "--all"]);
        self.git(&[
            "commit",
            "-q",
            "--allow-empty",
            "-m",
            message,
            "-m",
            &format!("Goal: {goal}"),
        ]);
    }
    // Writes files and commits them under `goal`.
    fn work(&self, files: &[(&str, &str)], goal: &str) {
        for (path, body) in files {
            self.write(path, body);
        }
        self.commit_all(&format!("work on {goal}"), goal);
    }
    fn head(&self) -> String {
        self.git(&["rev-parse", "HEAD"]).trim().to_owned()
    }
    // Observe the remote the way a user does: fetch, and move local main to the remote's main.
    fn refresh(&self) {
        if self.git(&["branch", "--show-current"]).trim() == "main" {
            self.git(&["switch", "-q", "--detach"]);
        }
        self.git(&[
            "fetch",
            "-q",
            "origin",
            "+refs/heads/*:refs/remotes/origin/*",
        ]);
        self.git(&["fetch", "-q", "origin", "+main:main"]);
    }
    fn branch(&self, name: &str) {
        self.refresh();
        self.git(&["switch", "-q", "-C", name, "origin/main"]);
    }
    fn claim_refs(&self) -> String {
        self.git(&["ls-remote", "origin", "refs/whosaidso/claims/*"])
    }
    fn fetch_tip(&self) -> String {
        self.git(&["ls-remote", "origin", "refs/heads/main"])
            .split_whitespace()
            .next()
            .unwrap_or("")
            .to_owned()
    }
}

// Runs the jobs together: every thread waits on one barrier, then sleeps a seeded 0..`spread` ms so that
// different rounds interleave the machines differently (a fixed seed keeps a failing round reproducible).
fn race_spread(jobs: &[(&Machine, &[&str])], seed: u64, spread: u64) -> Vec<Output> {
    let barrier = Barrier::new(jobs.len());
    std::thread::scope(|s| {
        let handles: Vec<_> = jobs
            .iter()
            .enumerate()
            .map(|(n, (m, args))| {
                let barrier = &barrier;
                let mut x = seed.wrapping_mul(0x9E37_79B9_7F4A_7C15)
                    ^ (n as u64 + 1).wrapping_mul(0xBF58_476D_1CE4_E5B9);
                for _ in 0..3 {
                    x ^= x << 13;
                    x ^= x >> 7;
                    x ^= x << 17;
                }
                let delay = if spread == 0 { 0 } else { x % spread };
                s.spawn(move || {
                    barrier.wait();
                    std::thread::sleep(std::time::Duration::from_millis(delay));
                    m.ws(args)
                })
            })
            .collect();
        handles.into_iter().map(|h| h.join().unwrap()).collect()
    })
}
fn race(jobs: &[(&Machine, &[&str])]) -> Vec<Output> {
    race_spread(jobs, 0, 0)
}

fn goal_json(parent: Option<&str>, order: i64, file: Option<&str>) -> Value {
    let mut goal =
        json!({"title":"goal","done_when":"works","parent":parent,"order":order,"after":[]});
    if let Some(file) = file {
        // Records which commit the check saw, so tests can prove every landed tip was tested.
        goal["check"] = json!([
            "sh",
            "-c",
            format!("test -f {file} && git rev-parse HEAD >> \"$TESTED\"")
        ]);
    }
    goal
}

// The first machine writes records, installs hooks, and publishes the project.
fn seed(m: &Machine, goals: &[(&str, Value)]) {
    m.write(".whosaidso/.gitignore", "cache/\n");
    m.write(
        ".whosaidso/config.json",
        json!({"focus":500,"limits":{"active":500}}).to_string(),
    );
    for (id, body) in goals {
        m.write(&format!(".whosaidso/goals/{id}.json"), body.to_string());
    }
    m.ok(&["install-hooks"]);
    m.ok(&["check"]);
    m.commit_all("Seed the ledger", "none");
    m.git(&["push", "-q", "--no-verify", "-u", "origin", "main"]);
    m.git(&["switch", "-q", "-c", "idle"]);
}

// Every receipt on main names a candidate the real checks ran against, and that candidate already contained the
// previous main tip: no landing ever skipped over or dropped another machine's landed work.
fn assert_tips_tested(world: &World, m: &Machine) {
    let tested = world.tested_candidates();
    let log = m.git(&[
        "log",
        "--first-parent",
        "--reverse",
        "--format=%H%x09%(trailers:key=WhoSaidSo-Receipt,valueonly)",
        "origin/main",
    ]);
    let mut receipts = 0;
    let mut previous: Option<String> = None;
    for line in log.lines() {
        let Some((commit, receipt)) = line.split_once('\t') else {
            continue;
        };
        if !receipt.is_empty() {
            receipts += 1;
            let v: Value = serde_json::from_str(receipt).unwrap();
            let candidate = v["candidate"].as_str().unwrap();
            assert!(
                v["goals"].as_array().unwrap().is_empty() || tested.iter().any(|t| t == candidate),
                "receipt {commit} was published for candidate {candidate} that no check ever ran against"
            );
            if let Some(previous) = &previous {
                let contained = m.git_out(&["merge-base", "--is-ancestor", previous, candidate]);
                assert!(
                    contained.status.success(),
                    "receipt {commit}: its tested candidate {candidate} does not contain the previous main tip {previous}"
                );
            }
        }
        previous = Some(commit.to_owned());
    }
    assert!(receipts > 0, "no receipt found on main");
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64
}

// Rewrites when the published claim on `goal` says it was last started, as the claimant's own clock would have
// stamped it: the claim blob is replaced on the remote and in this machine's mirrors of it.
fn restamp_claim(world: &World, holder: &Machine, goal: &str, since: i64) {
    let claim = world
        .claim(goal)
        .unwrap_or_else(|| panic!("no claim on {goal}"));
    let rewritten: String = claim
        .lines()
        .map(|l| {
            if l.starts_with("since ") {
                format!("since {since}\n")
            } else {
                format!("{l}\n")
            }
        })
        .collect();
    holder.write(".git/restamped-claim", &rewritten);
    let oid = holder
        .git(&["hash-object", "-w", ".git/restamped-claim"])
        .trim()
        .to_owned();
    holder.git(&[
        "push",
        "-q",
        "--no-verify",
        "--force",
        "origin",
        &format!("{oid}:refs/whosaidso/claims/{goal}"),
    ]);
    holder.git(&["update-ref", &format!("refs/whosaidso/claims/{goal}"), &oid]);
    holder.git(&[
        "update-ref",
        &format!("refs/whosaidso/origin-claims/{goal}"),
        &oid,
    ]);
}

// Distinct, valid PNG bytes of a given size.
fn image(tag: u8, size: usize) -> Vec<u8> {
    let mut payload = vec![tag; size];
    payload.extend_from_slice(&(size as u64).to_le_bytes());
    png(payload)
}

#[path = "multi_machine/claims.rs"]
mod claims;
#[path = "multi_machine/landing.rs"]
mod landing;
#[path = "multi_machine/records.rs"]
mod records;

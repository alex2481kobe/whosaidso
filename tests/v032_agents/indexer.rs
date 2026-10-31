use super::*;
use common::scip::{doc, field};

fn fixture() -> Fixture {
    let f = Fixture::sources(&[("main.go", "package example\nfunc target() {}\n")]);
    let mut bytes = field(1, &[]);
    bytes.extend(doc(
        "main.go",
        "scip-go gomod example v0.0.0 target().",
        "target",
        &[(&[1, 5, 11], true)],
    ));
    write(&f.root.join(".git/input.scip"), bytes);
    executable(
        &f.root.join(".git/bound-bin/scip-go"),
        r##"#!/usr/bin/python3
import pathlib,sys,time,subprocess
root=pathlib.Path.cwd()
if '--version' in sys.argv:
 if (root/'.git/hang-version').exists():time.sleep(30)
 print('scip-go 0.2.7');sys.exit()
if (root/'.git/hang').exists():
 subprocess.Popen([sys.executable,'-c',"import time,pathlib;time.sleep(1);pathlib.Path('.git/escaped').write_text('alive')"])
 (root/'.git/started').write_text('started')
 time.sleep(30)
out=pathlib.Path(sys.argv[sys.argv.index('--output')+1]);out.write_bytes((root/'.git/input.scip').read_bytes())
"##,
    );
    f
}
#[test]
fn timeout_cancels_the_process_group_and_retains_live_facts() {
    let f = fixture();
    cli(&f.root, &["code", "index", "go"]);
    write(&f.root.join(".git/hang"), "");
    let first = command(&f.root, BIN, &["code", "index", "go", "--timeout", "0.5"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    wait(&f.root.join(".git/started"));
    failure(
        command(&f.root, BIN, &["code", "index", "go"])
            .output()
            .unwrap(),
        "indexer busy",
    );
    failure(first.wait_with_output().unwrap(), "timed out");
    std::thread::sleep(std::time::Duration::from_millis(1100));
    assert!(
        !f.root.join(".git/escaped").exists(),
        "a descendant survived cancellation"
    );
    assert!(cli(&f.root, &["code", "defs", "target"]).contains("[precise]"));
    std::fs::rename(f.root.join(".git/hang"), f.root.join(".git/finished-hang")).unwrap();
    cli(&f.root, &["code", "index", "go"]);
}
#[test]
fn version_probe_has_a_deadline_and_invalid_timeouts_never_start_a_tool() {
    let f = fixture();
    write(&f.root.join(".git/hang-version"), "");
    bad(
        &f.root,
        &["code", "index", "go", "--timeout", "0.1"],
        "timed out",
    );
    for value in ["0", "-1", "NaN"] {
        bad(
            &f.root,
            &["code", "index", "go", "--timeout", value],
            "positive",
        );
    }
}

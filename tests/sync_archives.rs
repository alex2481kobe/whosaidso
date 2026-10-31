mod common;
use common::process::*;
use common::*;
use std::fs;

#[test]
fn monthly_bundles_merge_without_losing_either_clone() {
    let f = Fixture::new();
    let root = &f.root;

    let a = root.clone();
    f.remote();
    let b = f.clone("b");

    for (repo, payload) in [(&a, "first"), (&b, "second")] {
        attach(repo, payload);
        commit(repo);
        fs::remove_file(repo.join(".whosaidso/goals/g.json")).unwrap();
        commit(repo);
        cli(repo, &["prune"]);
    }
    cli(&a, &["sync"]);
    cli(&b, &["sync"]);
    cli(&a, &["sync"]);
    let bundle = fs::read_dir(a.join(".git/whosaidso/archive"))
        .unwrap()
        .map(|e| e.unwrap().path())
        .find(|p| p.extension().is_some_and(|e| e == "tar"))
        .unwrap();
    let other = b
        .join(".git/whosaidso/archive")
        .join(bundle.file_name().unwrap());
    assert_eq!(fs::read(&bundle).unwrap(), fs::read(other).unwrap());
    let listing = success(
        command(&a, "tar", &["-tf", bundle.to_str().unwrap()])
            .output()
            .unwrap(),
    );
    assert_eq!(listing.lines().count(), 4);
    for name in listing.lines().filter(|n| n.ends_with(".json")) {
        let v: serde_json::Value = serde_json::from_str(&success(
            command(&a, "tar", &["-xOf", bundle.to_str().unwrap(), name])
                .output()
                .unwrap(),
        ))
        .unwrap();
        let bytes = command(
            &a,
            "tar",
            &[
                "-xOf",
                bundle.to_str().unwrap(),
                &format!("{}.png", name.trim_end_matches(".json")),
            ],
        )
        .output()
        .unwrap();
        assert_eq!(hash(&bytes.stdout), v["media_sha256"]);
    }
}

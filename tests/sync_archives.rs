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
    let mut expected = std::collections::BTreeMap::new();

    for (repo, payload) in [(&a, "first"), (&b, "second")] {
        attach(repo, payload);
        for (path, bytes) in snapshot(&repo.join(".whosaidso/media")) {
            let id = path.file_stem().unwrap().to_str().unwrap();
            expected.insert(format!("{id}.json"), bytes);
            expected.insert(format!("{id}.png"), png(payload));
        }
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
        .find(|p| p.to_string_lossy().ends_with(".tar.gz"))
        .unwrap();
    let other = b
        .join(".git/whosaidso/archive")
        .join(bundle.file_name().unwrap());
    assert_eq!(fs::read(&bundle).unwrap(), fs::read(other).unwrap());
    assert_eq!(common::archive::entries(&bundle), expected);
}

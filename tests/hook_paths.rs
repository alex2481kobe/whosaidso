mod common;
use common::process::*;
use common::*;

#[test]
fn hook_installation_ignores_unrelated_paths_and_old_registry_directory() {
    let f = Fixture::empty();
    let registry = f.root.parent().unwrap().join("registry");
    write(&registry.join("projects/old-entry"), "old data");
    write(&f.root.join("target/whosaidso/keep"), "unrelated");
    std::os::unix::fs::symlink(BIN, f.root.join("whosaidso")).unwrap();
    let path = std::env::join_paths(
        std::iter::once(std::path::PathBuf::from(".")).chain(std::env::split_paths(tool_path())),
    )
    .unwrap();
    success(
        command(&f.root, "sh", &["-c", "whosaidso install-hooks"])
            .env("PATH", path)
            .output()
            .unwrap(),
    );
    assert_eq!(
        std::fs::read_to_string(registry.join("projects/old-entry")).unwrap(),
        "old data"
    );
    let entries = std::fs::read_to_string(registry.join("projects.list")).unwrap();
    assert_eq!(
        entries.trim(),
        f.root.canonicalize().unwrap().to_str().unwrap()
    );
}

#[test]
fn filesystem_failures_name_the_hook_or_registry_path() {
    let f = Fixture::empty();
    let path = f.root.join(".whosaidso/hooks");
    write(&path, "blocked");
    bad(&f.root, &["install-hooks"], path.to_str().unwrap());
    let other = Fixture::empty();
    let path = other.root.parent().unwrap().join("registry/projects.list");
    std::fs::create_dir_all(&path).unwrap();
    bad(&other.root, &["install-hooks"], path.to_str().unwrap());
}

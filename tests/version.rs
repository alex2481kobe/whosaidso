// `--version` and `-V` print the package version, even outside a Git repository.
use std::process::Command;
#[test]
fn version_flags_print_the_package_version() {
    let outside = std::env::temp_dir();
    for flag in ["--version", "-V"] {
        let out = Command::new(env!("CARGO_BIN_EXE_whosaidso")).arg(flag).current_dir(&outside).output().unwrap();
        assert!(out.status.success(), "{out:?}");
        assert_eq!(String::from_utf8_lossy(&out.stdout), format!("whosaidso {}\n", env!("CARGO_PKG_VERSION")));
    }
}

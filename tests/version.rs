mod common;
use common::process::*;
use common::*;

#[test]
fn version_flags_work_outside_git() {
    let f = Fixture::empty();
    let root = &f.root;

    for flag in ["--version", "-V"] {
        assert_eq!(
            cli(root.parent().unwrap(), &[flag]).trim(),
            format!("whosaidso {}", env!("CARGO_PKG_VERSION"))
        );
    }
}

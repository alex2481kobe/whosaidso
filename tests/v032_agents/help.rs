use super::*;
#[test]
fn command_help_works_outside_git() {
    let f = Fixture::empty();
    let outside = f.root.parent().unwrap();
    for args in [
        ["help", "grep"],
        ["help", "code"],
        ["help", "install-agent"],
    ] {
        let text = cli(outside, &args);
        assert!(
            text.contains(args[if args[0] == "help" { 1 } else { 0 }]),
            "{text}"
        );
    }
    write(&f.root.join("help.rs"), "fn help() {}\n");
    assert!(cli(&f.root, &["grep", "help"]).contains("help.rs"));
    bad(outside, &["help", "not-a-command"], "unknown help topic");
}

use super::*;
#[test]
fn command_help_and_flow_help_work_outside_git() {
    let f = Fixture::empty();
    let outside = f.root.parent().unwrap();
    for args in [
        ["help", "grep"],
        ["help", "code"],
        ["help", "flow"],
        ["flow", "help"],
        ["help", "install-agent"],
    ] {
        let text = cli(outside, &args);
        assert!(
            text.contains(args[if args[0] == "help" { 1 } else { 0 }]),
            "{text}"
        );
    }
    assert!(cli(outside, &["flow", "help"]).contains("drift-check"));
    write(&f.root.join("help.rs"), "fn help() {}\n");
    assert!(cli(&f.root, &["grep", "help"]).contains("help.rs"));
    bad(outside, &["help", "not-a-command"], "unknown help topic");
}

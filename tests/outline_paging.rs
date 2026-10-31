mod common;
use common::{process::{bad, cli}, write, Fixture};

#[test]
fn outline_can_page_and_filter_beyond_the_default_window() {
    let f = Fixture::empty();
    let source: String = (0..104).map(|n| format!("void ep_item_{n:03}(void) {{}}\n")).collect();
    write(&f.root.join("eventpoll.c"), format!("{source}void ep_remove(void) {{}}\n"));
    let first = cli(&f.root, &["code", "outline", "eventpoll.c"]);
    assert!(first.contains("shown 20 of 105 rows; offset 0; limit 20"), "{first}");
    let last = cli(&f.root, &["code", "outline", "eventpoll.c", "--offset", "100", "--limit", "10"]);
    assert!(last.contains("ep_remove") && last.contains("shown 5 of 105 rows; offset 100; limit 10"), "{last}");
    let filtered = cli(&f.root, &["code", "outline", "eventpoll.c", "remove"]);
    assert!(filtered.contains("ep_remove") && !filtered.contains("ep_item"), "{filtered}");
    let empty = cli(&f.root, &["code", "outline", "eventpoll.c", "missing"]);
    assert!(empty.contains("none recorded"), "{empty}");
    bad(&f.root, &["code", "outline", "eventpoll.c", "--limit", "0"], "positive integer");
    bad(&f.root, &["code", "outline", "eventpoll.c", "--offset", "bad"], "nonnegative integer");
}

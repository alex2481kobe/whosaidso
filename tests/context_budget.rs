mod common;
use common::{Fixture, process::cli, write};

#[test]
fn context_budget_includes_repeated_symbol_path() {
    let f = Fixture::empty();
    let path = format!("{}.rs", "p".repeat(242));
    assert_eq!(path.len(), 245);
    write(&f.root.join(path), "fn needle() {}\n");
    for budget in [128, 256, 512, 800] {
        let out = cli(
            &f.root,
            &["context", "needle", "--budget", &budget.to_string()],
        );
        assert!(
            out.len() <= budget,
            "budget {budget}, {} bytes: {out}",
            out.len()
        );
        assert!(out.contains("omitted:"), "{out}");
    }
}

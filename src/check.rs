// Repository record checks; diagnostics include every broken record.
pub mod budgets;
use std::path::Path;
pub fn check(root: &Path) -> Result<(), String> {
    if crate::prune::journal_path(root)?.exists() {
        println!("warning: unfinished prune; run whosaidso prune");
    }
    // Older event ledgers are history in Git, not records; a green check must not imply they were read.
    if root.join(".whosaidso/events").is_dir() {
        println!(
            "warning: older ledger in .whosaidso/events was not read; its history stays in Git"
        );
    }
    let store = crate::record::load(root)?;
    let budgets = budgets::problems(root, &store)?;
    for message in store.warnings.iter().chain(&store.errors).chain(&budgets) {
        println!("{message}");
    }
    if !store.errors.is_empty() || !budgets.is_empty() {
        return Err(String::new());
    }
    println!(
        "check: ok ({} goals, {} proofs)",
        store.goals.len(),
        store.proofs.len()
    );
    Ok(())
}

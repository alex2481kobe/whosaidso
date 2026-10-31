// Repository record checks; diagnostics include every broken record.
use std::path::Path;
pub fn check(root: &Path) -> Result<(), String> {
    if crate::prune::journal_path(root)?.exists() {
        println!("warning: unfinished prune; run whosaidso prune");
    }
    let store = crate::record::load(root)?;
    for message in store.warnings.iter().chain(&store.errors) {
        println!("{message}");
    }
    if !store.errors.is_empty() {
        return Err(String::new());
    }
    println!(
        "check: ok ({} goals, {} proofs)",
        store.goals.len(),
        store.proofs.len()
    );
    Ok(())
}

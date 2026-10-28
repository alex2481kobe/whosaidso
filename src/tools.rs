// Lists recorded instruments and their declared gaps.
use crate::record::load;
use std::path::Path;

pub fn tools(root: &Path) -> Result<(), String> {
    let mut store = load(root)?;
    if !store.errors.is_empty() {
        for error in &store.errors {
            println!("{error}");
        }
        return Err(String::new());
    }
    store.instruments.sort_by(|a, b| a.id.cmp(&b.id));
    for instrument in &store.instruments {
        println!("{}  {}  — {}", instrument.id, serde_json::to_string(&instrument.run).map_err(|e| e.to_string())?, instrument.answers);
        for gap in &instrument.gaps {
            println!("    gap: {} (covered by {}: {})", gap.what, gap.covered_by, gap.why);
        }
    }
    println!("{} tools", store.instruments.len());
    Ok(())
}

// Sync and check command reporting.
mod transport;
use std::path::Path;
pub fn sync(root: &Path, remote: &str) -> Result<(), String> {
    let (received, sent) = transport::transfer(root, remote)?;
    println!("sync: {received} received, {sent} sent");
    Ok(())
}
pub fn check(root: &Path) -> Result<(), String> {
    let count = transport::missing(root)?;
    if count > 0 {
        println!("warning: {count} media missing here; run whosaidso sync");
    }
    crate::check::check(root)
}

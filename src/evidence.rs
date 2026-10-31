// Attachments use the media owner for storage, validation and gallery publication.
use std::path::Path;
pub fn add(root: &Path, cwd: &Path, goal: &str, file: &str, shows: &str) -> Result<(), String> {
    if let Some(milestone) = crate::record::load(root)?.folded.get(goal) {
        return Err(format!(
            "evidence: reopen {milestone} before updating folded evidence"
        ));
    }
    // Single media integration point; arbitrary file support belongs to that module.
    crate::gallery::show(root, cwd, goal, file, shows)
}

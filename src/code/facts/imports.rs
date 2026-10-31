// Import reads use the same selected fact generation as symbol reads.
use super::Snapshot;
use std::path::Path;
impl Snapshot {
    pub fn dependencies(&self, path: &str) -> Result<Vec<String>, String> {
        super::super::store::reads::dependencies(&self.db, path)
    }
    pub fn includers(&self, path: &str) -> Result<Vec<String>, String> {
        let name = Path::new(path)
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("");
        Ok(super::super::store::reads::includers(&self.db)?
            .into_iter()
            .filter(|(_, _, target)| {
                !name.is_empty()
                    && (target == name
                        || target.strip_suffix(name).is_some_and(|p| p.ends_with('/')))
            })
            .map(|(path, line, _)| format!("included by {path}:{line}  [syntactic, by name]"))
            .collect())
    }
}

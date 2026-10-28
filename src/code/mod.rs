// Code graph commands; parsing, storage and presentation live in their own modules.
mod precise;
mod parse;
mod query;
mod store;

use std::path::Path;

pub fn query(root: &Path, command: &str, value: &str) -> Result<(), String> {
    let (_, files) = store::index(root)?;
    query::run(root, command, value, &files)
}

pub fn import_scip(root: &Path, file: &Path) -> Result<(), String> { precise::import(root, file) }

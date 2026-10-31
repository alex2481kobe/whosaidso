// Code search commands; parsing, storage and presentation live in their own modules.
mod contracts;
pub mod facts;
mod languages;
mod parse;
mod precise;
mod query;
mod store;

use std::path::Path;

pub fn query(root: &Path, command: &str, value: &str) -> Result<(), String> {
    query::run(root, command, value)
}

pub fn import_scip(root: &Path, file: &Path) -> Result<(), String> {
    let report = precise::storage::import(root, file)?;
    println!("{report}");
    Ok(())
}

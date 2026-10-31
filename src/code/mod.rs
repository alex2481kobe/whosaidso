// Code search commands; parsing, storage and presentation live in their own modules.
pub(crate) mod contracts;
pub mod facts;
mod impact;
mod indexer;
mod inspect;
pub(crate) mod languages;
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

pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    match args {
        [sub, topic] if sub == "help" && topic == "coverage" => {
            println!(
                "Syntax limits are from extractor rules. SCIP guidance is from indexer coverage notes, not locally reproduced; a missing row is unknown, never proof of absence."
            );
            for language in languages::LANGUAGES {
                println!(
                    "{}: syntax: {}; SCIP guidance: {}",
                    language.name, language.syntax_gap, language.precise_gap
                );
            }
            Ok(())
        }

        [sub, value, rest @ ..] if ["callers", "callees", "affected"].contains(&sub.as_str()) => {
            println!("{}", impact::answer(root, sub, value, rest)?);
            Ok(())
        }
        [sub, language] if sub == "index" => {
            println!("{}", indexer::run(root, language)?);
            Ok(())
        }
        [sub, value] if ["show", "outline"].contains(&sub.as_str()) => {
            let snapshot = facts::Snapshot::current(root)?;
            println!("{}", inspect::answer(&snapshot, sub, value)?);
            Ok(())
        }
        [sub, value] => query(root, sub, value),
        _ => Err("usage: code defs|refs|deps|includers|show|outline|index <value>; callers|callees|affected <symbol|path> [--depth N] [--limit N] [--offset N] [--snapshot ID] [--implementations]; help coverage".into()),
    }
}

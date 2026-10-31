// Command boundary for topic notes and revision-aware edits.
pub mod model;
pub mod reconcile;
pub mod store;
use crate::{git, record};
use model::{Note, Status};
use std::{fs, path::Path};

fn get(root: &Path, id: &str) -> Result<Note, String> {
    store::load(root)?
        .into_iter()
        .find(|record| record.id == id)
        .ok_or_else(|| format!("unknown note {id}"))?
        .note
}
pub fn command(root: &Path, args: &[String]) -> Result<(), String> {
    match args {
        [cmd] if cmd == "list" => {
            let mut bad = false;
            for record in store::load(root)? {
                let id = record.id;
                match record.note {
                    Ok(n) => println!("{} {} {} revision={} {}", model::label(&n.header.status), model::label(&n.header.kind), id, n.revision, n.header.title),
                    Err(e) => { println!("broken {id}: {e}"); bad = true; }
                }
            }
            if bad { return Err(String::new()); }
        }
        [cmd, id] if cmd == "show" => {
            let n = get(root, id)?;
            println!("revision={}\n{}", n.revision, String::from_utf8_lossy(&model::encode(&n)?));
        }
        [cmd, id] if cmd == "history" => {
            if !record::valid_id(id) { return Err("invalid note id".into()); }
            print!("{}", git::run(root, &["log", "--format=%h %s", "--", &format!(".whosaidso/notes/{id}.md")])?);
        }
        [cmd, id, revision, file] if cmd == "put" => {
            let bytes = fs::read(root.join(file)).map_err(|e| e.to_string())?;
            let note = model::decode(id, &bytes)?;
            let rev = store::change(root, id, revision, |_| Ok(note))?;
            println!("note {id} revision={rev}");
        }
        [cmd, id, revision] if matches!(cmd.as_str(), "open" | "resolve" | "retire") => {
            let rev = store::change(root, id, revision, |note| {
                let mut note = note.ok_or("unknown note")?;
                note.header.status = match cmd.as_str() { "open" => Status::Open, "resolve" => Status::Resolved, _ => Status::Retired };
                Ok(note)
            })?;
            println!("note {id} revision={rev}");
        }
        [cmd, id, revision, destination] if cmd == "reconcile" => {
            if !record::hex(destination, 64) { return Err("invalid destination note revision".into()); }
            let rev = store::change(root, id, revision, |note| {
                let mut note = note.ok_or("unknown note")?;
                if !note.header.reconciled.contains(destination) { note.header.reconciled.push(destination.clone()); }
                Ok(note)
            })?;
            println!("note {id} reconciled revision={rev}");
        }
        _ => return Err("usage: note show|history <slug> | list | put <slug> <revision|new> <file> | open|resolve|retire <slug> <revision> | reconcile <slug> <revision> <destination-note-revision>".into()),
    }
    Ok(())
}

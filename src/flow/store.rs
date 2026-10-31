use super::{model::Flow, schema, validate};
use std::{fs, path::Path};

pub struct Record {
    pub id: String,
    pub flow: Option<Flow>,
    pub problems: Vec<String>,
    pub revision: String,
}

pub fn load(root: &Path, selected: Option<&str>) -> Result<Vec<Record>, String> {
    if let Some(id) = selected
        && !validate::valid_id(id)
    {
        return Err(format!("invalid flow id {id:?}"));
    }
    let folder = root.join(".whosaidso/flows");
    let mut files = if let Some(id) = selected {
        vec![folder.join(format!("{id}.json"))]
    } else {
        match fs::read_dir(&folder) {
            Ok(entries) => entries
                .map(|entry| entry.map(|e| e.path()))
                .collect::<Result<Vec<_>, _>>()
                .map_err(|e| e.to_string())?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => vec![],
            Err(e) => return Err(e.to_string()),
        }
    };
    files.retain(|path| path.extension().is_some_and(|e| e == "json"));
    files.sort();
    let mut records = vec![];
    for path in files {
        let id = path.file_stem().unwrap().to_string_lossy().into_owned();
        records.push(decode(id, fs::read(&path).map_err(|e| e.to_string())));
    }
    Ok(records)
}

fn decode(id: String, bytes: Result<Vec<u8>,String>) -> Record {
    let mut problems = vec![];
    if !validate::valid_id(&id) { problems.push("invalid flow filename id".into()); }
    let mut revision = String::new();
    let flow = match bytes.and_then(|bytes| {
        revision = crate::record::sha256_hex(&bytes);
        serde_json::from_slice(&bytes).map_err(|e| e.to_string())
    }) {
        Ok(value) => {
            let (flow, schema_errors) = schema::decode(&value);
            problems.extend(schema_errors);
            problems.extend(validate::validate(&flow));
            Some(flow)
        }
        Err(e) => { problems.push(e); None }
    };
    Record { id,flow,problems,revision }
}

pub fn change(root: &Path, id: &str, change: super::author::Change) -> Result<(), String> {
    use super::author::{self, Change};
    if !validate::valid_id(id) {
        return Err(format!("invalid flow id {id:?}"));
    }
    crate::publish::record_dir(root, "flows")?;
    let git_dir = root.join(crate::git::run(root, &["rev-parse", "--absolute-git-dir"])?.trim());
    let lock_dir = git_dir.join("whosaidso");
    fs::create_dir_all(&lock_dir).map_err(|e| e.to_string())?;
    let lock = fs::File::options()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(lock_dir.join("flows.lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    let target = root.join(".whosaidso/flows").join(format!("{id}.json"));
    let exists = match fs::symlink_metadata(&target) {
        Ok(meta) if !meta.is_file() || meta.file_type().is_symlink() => {
            return Err("flow file is a symlink or not a regular file".into());
        }
        Ok(_) => true,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => false,
        Err(e) => return Err(e.to_string()),
    };
    if !exists && matches!(change, Change::Remove { .. } | Change::Delete) {
        return Err(format!("unknown flow {id:?}"));
    }
    let current = if exists && !matches!(change, Change::Replace(_) | Change::Delete) {
        let record = load(root, Some(id))?.pop().ok_or("flow disappeared")?;
        if !record.problems.is_empty() {
            return Err(format!("{id} broken\n{}", record.problems.join("\n")));
        }
        record.flow
    } else {
        None
    };
    match author::apply(current, change)? {
        Some(flow) => {
            let mut bytes = serde_json::to_vec_pretty(&flow).map_err(|e| e.to_string())?;
            bytes.push(b'\n');
            crate::publish::write_bytes(&target, &bytes)
        }
        None => fs::remove_file(target).map_err(|e| e.to_string()),
    }
}

// The machine registry contains repository roots, never project state.
use crate::git;
use serde::Serialize;
use std::{
    fs,
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
};

fn registry() -> Result<PathBuf, String> {
    let home = std::env::var_os("WHOSAIDSO_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".whosaidso")))
        .ok_or("projects: HOME or WHOSAIDSO_HOME is required")?;
    Ok(std::env::current_dir()
        .map_err(|e| e.to_string())?
        .join(home)
        .join("projects"))
}
pub fn register_viewer(root: &Path) -> Result<(), String> {
    let mut ancestor = registry()?;
    while !ancestor.exists() {
        if !ancestor.pop() {
            return Err("invalid projects registry path".into());
        }
    }
    if fs::canonicalize(ancestor)
        .map_err(|e| e.to_string())?
        .starts_with(fs::canonicalize(root).map_err(|e| e.to_string())?)
    {
        return Err(
            "ui: projects registry must be outside the repository (HOME or WHOSAIDSO_HOME)".into(),
        );
    }
    register(root)
}
pub fn register(root: &Path) -> Result<(), String> {
    let root = git::root(root)?;
    let text = root
        .to_str()
        .ok_or("projects: repository path is not UTF-8")?;
    if text.contains(['\n', '\r']) {
        return Err("projects: repository path contains a newline".into());
    }
    let path = registry()?;
    fs::create_dir_all(path.parent().ok_or("invalid registry path")?).map_err(|e| e.to_string())?;
    let mut file = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(path)
        .map_err(|e| e.to_string())?;
    file.lock().map_err(|e| e.to_string())?;
    let mut entries = String::new();
    file.read_to_string(&mut entries)
        .map_err(|e| e.to_string())?;
    if !entries.lines().any(|p| p == text) {
        file.seek(SeekFrom::End(0)).map_err(|e| e.to_string())?;
        if !entries.is_empty() && !entries.ends_with('\n') {
            file.write_all(b"\n").map_err(|e| e.to_string())?;
        }
        writeln!(file, "{text}").map_err(|e| e.to_string())?;
    }
    Ok(())
}
#[derive(Serialize)]
pub struct Project {
    pub id: String,
    pub name: String,
    pub available: bool,
    pub reason: String,
}
pub fn list() -> Result<Vec<Project>, String> {
    let entries = match fs::read_to_string(registry()?) {
        Ok(s) => s,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => String::new(),
        Err(e) => return Err(e.to_string()),
    };
    let mut seen = std::collections::HashSet::new();
    let mut projects = Vec::new();
    for entry in entries.lines().filter(|s| !s.is_empty()) {
        if !seen.insert(entry) {
            continue;
        }
        let path = Path::new(entry);
        let reason = if !path.is_absolute() {
            "Path is not absolute"
        } else if !path.is_dir() {
            "Repository is missing"
        } else if git::root(path).as_deref() != Ok(path) {
            "Not a repository root"
        } else if !fs::symlink_metadata(path.join(".whosaidso"))
            .is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
        {
            "Not a whosaidso repository"
        } else {
            ""
        };
        projects.push(Project {
            id: entry.into(),
            name: path
                .file_name()
                .unwrap_or(path.as_os_str())
                .to_string_lossy()
                .into_owned(),
            available: reason.is_empty(),
            reason: reason.into(),
        });
    }
    Ok(projects)
}

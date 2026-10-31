// Embedded public skills and ownership checks for client installation.
use std::{fs, path::Path};

const SKILLS: &[(&str, &str)] = &[
    (
        "whosaidso-goals",
        include_str!("../../skills/whosaidso-goals/SKILL.md"),
    ),
    (
        "whosaidso-search",
        include_str!("../../skills/whosaidso-search/SKILL.md"),
    ),
    (
        "whosaidso-flows",
        include_str!("../../skills/whosaidso-flows/SKILL.md"),
    ),
    (
        "whosaidso-landing",
        include_str!("../../skills/whosaidso-landing/SKILL.md"),
    ),
    (
        "whosaidso-gallery",
        include_str!("../../skills/whosaidso-gallery/SKILL.md"),
    ),
];

pub(super) fn check(folder: &Path) -> Result<(), String> {
    for (name, content) in SKILLS {
        let dir = folder.join(name);
        let file = dir.join("SKILL.md");
        for path in [&dir, &file] {
            match fs::symlink_metadata(path) {
                Ok(meta) if meta.file_type().is_symlink() => {
                    return Err(format!("existing skill is a symlink: {}", path.display()));
                }
                Ok(_) => {}
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(e) => return Err(format!("{}: {e}", path.display())),
            }
        }
        match fs::read(&file) {
            Ok(bytes) if bytes != content.as_bytes() => {
                return Err(format!(
                    "existing skill differs: {}; move it aside before retrying",
                    file.display()
                ));
            }
            Ok(_) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(format!("{}: {e}", file.display())),
        }
    }
    Ok(())
}

pub(super) fn apply(folder: &Path, uninstall: bool) -> Result<(), String> {
    for (name, content) in SKILLS {
        let dir = folder.join(name);
        let file = dir.join("SKILL.md");
        if uninstall {
            match fs::remove_file(&file) {
                Ok(()) => {}
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
                Err(e) => return Err(format!("{}: {e}", file.display())),
            }
            // Extra files belong to the user; only remove an empty skill directory.
            if fs::read_dir(&dir)
                .map_err(|e| format!("{}: {e}", dir.display()))?
                .next()
                .is_none()
            {
                fs::remove_dir(&dir).map_err(|e| format!("{}: {e}", dir.display()))?;
            }
        } else if !file.exists() {
            fs::create_dir_all(&dir).map_err(|e| format!("{}: {e}", dir.display()))?;
            fs::write(&file, content).map_err(|e| format!("{}: {e}", file.display()))?;
        }
    }
    Ok(())
}

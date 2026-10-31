// The system gzip keeps compression outside the CLI's dependency tree.
use std::{
    io::Write,
    path::Path,
    process::{Command, Stdio},
};

fn transform(bytes: &[u8], decode: bool) -> Result<Vec<u8>, String> {
    if decode && bytes.is_empty() {
        return Ok(Vec::new());
    }
    let mut child = Command::new("gzip")
        .args(if decode {
            &["-d", "-c"][..]
        } else {
            &["-n", "-c"][..]
        })
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("gzip is required for media bundles: {e}"))?;
    let mut stdin = child.stdin.take().ok_or("missing gzip stdin")?;
    let (written, output) = std::thread::scope(|scope| {
        let writer = scope.spawn(move || stdin.write_all(bytes));
        let output = child.wait_with_output();
        (writer.join(), output)
    });
    let output = output.map_err(|e| e.to_string())?;
    if !output.status.success() {
        return Err(format!(
            "gzip: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        ));
    }
    written
        .map_err(|_| "gzip input thread failed")?
        .map_err(|e| e.to_string())?;
    Ok(output.stdout)
}
pub(super) fn encode(bytes: &[u8]) -> Result<Vec<u8>, String> {
    transform(bytes, false)
}
pub(super) fn decode(bytes: &[u8]) -> Result<Vec<u8>, String> {
    transform(bytes, true)
}

pub(super) fn compress_file(source: &Path, target: &Path) -> Result<(), String> {
    let output = std::fs::File::options()
        .create_new(true)
        .write(true)
        .open(target)
        .map_err(|e| e.to_string())?;
    let result = Command::new("gzip")
        .args(["-n", "-c"])
        .arg(source)
        .stdin(Stdio::null())
        .stdout(output)
        .stderr(Stdio::piped())
        .output()
        .map_err(|e| format!("gzip is required for media bundles: {e}"))?;
    if !result.status.success() {
        return Err(format!(
            "gzip: {}",
            String::from_utf8_lossy(&result.stderr).trim()
        ));
    }
    std::fs::File::open(target)
        .and_then(|f| f.sync_all())
        .map_err(|e| e.to_string())
}

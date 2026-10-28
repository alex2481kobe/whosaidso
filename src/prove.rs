// Run a goal's instrument and store its content-addressed proof.
use crate::{git, publish, record::{self, Instrument, Proof}};
use sha2::{Digest, Sha256};
use std::{collections::VecDeque, fs, io::Read, path::Path, process::{Command, Stdio}, os::{fd::OwnedFd, unix::net::UnixStream}};

pub const MAX_OUTPUT_TAIL_BYTES: usize = 16 * 1024;

fn clean(root: &Path) -> Result<bool, String> {
    Ok(git::run(root, &["status", "--porcelain", "--", ".", ":(exclude).whosaidso"])?.is_empty()
        && git::run(root, &["diff", "HEAD", "--name-only", "--", ".whosaidso"])?.is_empty())
}
fn head_reflog(root: &Path) -> Result<Vec<u8>, String> {
    let reason = "prove: HEAD's reflog is off or unreadable (core.logAllRefUpdates); can't tell whether HEAD moved during the run";
    if git::run(root, &["config", "--type=bool", "--get", "core.logAllRefUpdates"]).is_ok_and(|v| v.trim() == "false") {
        return Err(reason.into());
    }
    // `git reflog show HEAD` can fall back to the branch log when HEAD's log is missing.
    let path = git::run(root, &["rev-parse", "--git-path", "logs/HEAD"]).map_err(|_| reason.to_string())?;
    let log = fs::read(root.join(path.strip_suffix('\n').unwrap_or(&path))).map_err(|_| reason.to_string())?;
    if log.is_empty() { return Err(reason.into()); }
    Ok(log)
}
pub fn prove(root: &Path, id: &str) -> Result<(), String> {
    publish::record_dir(root, "proofs")?;
    if !clean(root)? { return Err("prove: commit or stash your changes first".into()); }
    let store = record::load(root)?;
    let goal = store.goals.iter().find(|g| g.id == id).ok_or_else(|| if store.errors.is_empty() { format!("unknown goal {id}") } else { store.errors.join("\n") })?;
    let mut pending = vec![goal.check.as_str()];
    let mut seen = std::collections::HashSet::new();
    while let Some(id) = pending.pop() {
        if !seen.insert(id) { continue; }
        let instrument = store.instruments.iter().find(|i| i.id == id).ok_or_else(|| format!("unknown instrument {id}"))?;
        run(root, &goal.id, instrument)?;
        pending.extend(instrument.gaps.iter().rev().map(|g| g.covered_by.as_str()));
    }
    Ok(())
}
fn run(root: &Path, id: &str, instrument: &Instrument) -> Result<(), String> {
    publish::record_dir(root, "proofs")?;
    if !clean(root)? { return Err("prove: commit or stash your changes first".into()); }
    let reflog = head_reflog(root)?;
    let commit = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    let bytes = git::run_bytes(root, &["show", &format!("{commit}:.whosaidso/instruments/{}.json", instrument.id)])?;
    let recorded: Instrument = serde_json::from_slice(&bytes).map_err(|e| format!("prove: instrument: {e}"))?;
    let (mut output, pipe) = UnixStream::pair().map_err(|e| e.to_string())?;
    let stderr = pipe.try_clone().map_err(|e| e.to_string())?;
    let child = Command::new(recorded.run.first().ok_or("prove: empty run")?).args(&recorded.run[1..]).current_dir(root)
        .stdout(Stdio::from(OwnedFd::from(pipe))).stderr(Stdio::from(OwnedFd::from(stderr))).spawn();
    let mut hash = Sha256::new();
    let mut tail = VecDeque::with_capacity(MAX_OUTPUT_TAIL_BYTES);
    let exit_code = match child {
        Ok(mut child) => {
            let mut buf = [0; 8192];
            loop {
                let n = output.read(&mut buf).map_err(|e| format!("prove: read output: {e}"))?;
                if n == 0 { break; }
                hash.update(&buf[..n]);
                let excess = (tail.len() + n).saturating_sub(MAX_OUTPUT_TAIL_BYTES);
                tail.drain(..excess);
                tail.extend(&buf[..n]);
            }
            let status = child.wait().map_err(|e| format!("prove: wait: {e}"))?;
            i64::from(status.code().unwrap_or(-1))
        }
        Err(e) => return Err(format!("prove: start instrument: {e}")),
    };
    if git::run(root, &["rev-parse", "HEAD"])?.trim() != commit { return Err("prove: HEAD changed during instrument".into()); }
    if !clean(root)? { return Err("prove: working tree changed during instrument".into()); }
    if head_reflog(root)? != reflog {
        return Err("prove: HEAD reflog changed during instrument; no proof recorded".into());
    }
    let mut output_tail = String::from_utf8_lossy(&tail.into_iter().collect::<Vec<_>>()).into_owned();
    // Lossy decoding can expand invalid bytes; retain a UTF-8 suffix within the same byte cap.
    let mut start = output_tail.len().saturating_sub(MAX_OUTPUT_TAIL_BYTES);
    while !output_tail.is_char_boundary(start) { start += 1; }
    output_tail.drain(..start);
    let mut proof = Proof {
        sha: String::new(), goal: id.into(), instrument: instrument.id.clone(),
        commit, exit_code,
        output_sha256: hash.finalize().iter().map(|b| format!("{b:02x}")).collect(),
        output_tail,
        ran_at: publish::ran_at()?,
    };
    // JSON escapes control bytes, so budget the serialized suffix, including its newline.
    let size = serde_json::to_vec(&proof).map_err(|e| e.to_string())?.len() + 1;
    let mut excess = size.saturating_sub(record::MAX_RECORD_BYTES as usize);
    let mut start = 0;
    for c in proof.output_tail.chars() {
        if excess == 0 { break; }
        let escaped = serde_json::to_vec(&c.to_string()).map_err(|e| e.to_string())?.len() - 2;
        excess = excess.saturating_sub(escaped);
        start += c.len_utf8();
    }
    proof.output_tail.drain(..start);
    let sha = publish::write_new(&root.join(".whosaidso/proofs"), &proof)?;
    println!("prove: {id} {} {}", record::result(exit_code), &sha[..12]);
    Ok(())
}

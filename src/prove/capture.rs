// Bounded combined output, a digest of the whole stream, and a process-group deadline.
use crate::record::{self, model::Run};
use sha2::{Digest, Sha256};
use std::{
    collections::VecDeque,
    io::Read,
    os::{
        fd::OwnedFd,
        unix::{net::UnixStream, process::CommandExt},
    },
    path::Path,
    process::{Command, Stdio},
    time::{Duration, Instant},
};
pub(super) fn run(
    tree: &Path,
    output_dir: &Path,
    target_dir: &Path,
    commit: &str,
    argv: &[String],
    limit: Option<f64>,
) -> Result<Run, String> {
    let (mut output, pipe) = UnixStream::pair().map_err(|e| e.to_string())?;
    let stderr = pipe.try_clone().map_err(|e| e.to_string())?;
    let start = Instant::now();
    let child = Command::new(argv.first().ok_or("prove: empty check")?)
        .args(&argv[1..])
        .current_dir(tree)
        .env("WHOSAIDSO_OUTPUT", output_dir)
        .env("TMPDIR", output_dir)
        .env("CARGO_TARGET_DIR", target_dir)
        .process_group(0)
        .stdin(Stdio::null())
        .stdout(Stdio::from(OwnedFd::from(pipe)))
        .stderr(Stdio::from(OwnedFd::from(stderr)))
        .spawn();
    let (exit_code, hash, text, timed_out) = match child {
        Ok(mut child) => {
            let reader = std::thread::spawn(move || {
                let mut hash = Sha256::new();
                let mut tail = VecDeque::new();
                let mut buf = [0; 8192];
                loop {
                    let n = output.read(&mut buf).map_err(|e| e.to_string())?;
                    if n == 0 {
                        break;
                    }
                    hash.update(&buf[..n]);
                    let excess = (tail.len() + n).saturating_sub(record::MAX_OUTPUT_TAIL_BYTES);
                    tail.drain(..excess);
                    tail.extend(&buf[..n]);
                }
                Ok::<_, String>((
                    hash,
                    String::from_utf8_lossy(&tail.into_iter().collect::<Vec<_>>()).into_owned(),
                ))
            });
            let mut status = None;
            let mut timeout = false;
            while status.is_none() || !reader.is_finished() {
                if status.is_none() {
                    status = child.try_wait().map_err(|e| e.to_string())?;
                }
                if limit.is_some_and(|s| start.elapsed().as_secs_f64() >= s) {
                    unsafe extern "C" {
                        fn kill(pid: i32, signal: i32) -> i32;
                    }
                    unsafe {
                        kill(-(child.id() as i32), 9);
                    }
                    status = Some(child.wait().map_err(|e| e.to_string())?);
                    timeout = true;
                    break;
                }
                if status.is_none() || !reader.is_finished() {
                    std::thread::sleep(Duration::from_millis(2));
                }
            }
            let (hash, text) = reader.join().map_err(|_| "check output reader failed")??;
            (
                if timeout {
                    -1
                } else {
                    i64::from(status.ok_or("missing check exit")?.code().unwrap_or(-1))
                },
                hash,
                text,
                timeout,
            )
        }
        Err(e) => {
            let text = format!("start check: {e}");
            let mut hash = Sha256::new();
            hash.update(text.as_bytes());
            (-1, hash, text, false)
        }
    };
    // Both escaped tails must fit in a single evidence record.
    let mut tail = String::new();
    let mut size = 0;
    for c in text.chars().rev() {
        let n = serde_json::to_vec(&c.to_string())
            .map_err(|e| e.to_string())?
            .len()
            - 2;
        if size + n > 28 * 1024 || tail.len() + c.len_utf8() > record::MAX_OUTPUT_TAIL_BYTES {
            break;
        }
        size += n;
        tail.push(c);
    }
    Ok(Run {
        commit: commit.into(),
        exit_code,
        output_sha256: hash.finalize().iter().map(|b| format!("{b:02x}")).collect(),
        output_tail: tail.chars().rev().collect(),
        elapsed_ms: start.elapsed().as_millis().try_into().unwrap_or(u64::MAX),
        timed_out,
    })
}

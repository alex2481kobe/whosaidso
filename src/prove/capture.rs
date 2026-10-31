// Bounded combined output, with a digest of the whole stream.
use crate::record::{self, model::Run};
use sha2::{Digest, Sha256};
use std::{
    collections::VecDeque,
    io::Read,
    os::{fd::OwnedFd, unix::net::UnixStream},
    path::Path,
    process::{Command, Stdio},
    time::Instant,
};
pub(super) fn run(
    tree: &Path,
    output_dir: &Path,
    commit: &str,
    argv: &[String],
) -> Result<Run, String> {
    let (mut output, pipe) = UnixStream::pair().map_err(|e| e.to_string())?;
    let stderr = pipe.try_clone().map_err(|e| e.to_string())?;
    let start = Instant::now();
    let child = Command::new(argv.first().ok_or("prove: empty check")?)
        .args(&argv[1..])
        .current_dir(tree)
        .env("WHOSAIDSO_OUTPUT", output_dir)
        .env("TMPDIR", output_dir)
        .env("CARGO_TARGET_DIR", output_dir.join("target"))
        .stdin(Stdio::null())
        .stdout(Stdio::from(OwnedFd::from(pipe)))
        .stderr(Stdio::from(OwnedFd::from(stderr)))
        .spawn();
    let mut hash = Sha256::new();
    let mut tail = VecDeque::with_capacity(record::MAX_OUTPUT_TAIL_BYTES);
    let exit_code = match child {
        Ok(mut child) => {
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
            i64::from(
                child
                    .wait()
                    .map_err(|e| e.to_string())?
                    .code()
                    .unwrap_or(-1),
            )
        }
        Err(e) => {
            let bytes = format!("start check: {e}");
            hash.update(bytes.as_bytes());
            tail.extend(bytes.bytes());
            -1
        }
    };
    let text = String::from_utf8_lossy(&tail.into_iter().collect::<Vec<_>>()).into_owned();
    // Escaping can expand each byte to six bytes; both runs must fit in one record.
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
    })
}

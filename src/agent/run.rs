// Exact single-file compatibility adapters. Unhandled file encodings use the original utility.
use super::redirect::supported;
use std::{fs, io::Write, process::Command};

pub(super) fn command(args: &[String]) -> Result<i32, String> {
    if !supported(&args.iter().map(String::as_str).collect::<Vec<_>>()) {
        return Err("agent-run: unsupported arguments".into());
    }
    let path = args.last().ok_or("missing path")?;
    let bytes = super::redirect::available(&args[0])
        .then(|| {
            fs::metadata(path)
                .ok()
                .filter(|m| m.is_file())
                .and_then(|_| fs::read(path).ok())
        })
        .flatten();
    if let Some(bytes) = bytes {
        if args[0] == "cat" {
            std::io::stdout()
                .write_all(&bytes)
                .map_err(|e| e.to_string())?;
            return Ok(0);
        }
        if !bytes.starts_with(&[0xef, 0xbb, 0xbf])
            && !bytes.contains(&0)
            && !bytes.contains(&b'\r')
            && std::str::from_utf8(&bytes).is_ok()
            && std::env::var_os("RIPGREP_CONFIG_PATH").is_none()
        {
            let pattern = args[4].as_bytes();
            let mut found = false;
            let mut out = std::io::BufWriter::new(std::io::stdout());
            for (line, bytes) in bytes.split_inclusive(|b| *b == b'\n').enumerate() {
                if bytes.windows(pattern.len()).any(|w| w == pattern) {
                    write!(out, "{}:", line + 1).map_err(|e| e.to_string())?;
                    out.write_all(bytes).map_err(|e| e.to_string())?;
                    if !bytes.ends_with(b"\n") {
                        out.write_all(b"\n").map_err(|e| e.to_string())?;
                    }
                    found = true;
                }
            }
            out.flush().map_err(|e| e.to_string())?;
            return Ok(if found { 0 } else { 1 });
        }
    }
    Command::new(&args[0])
        .args(&args[1..])
        .status()
        .map(|s| s.code().unwrap_or(1))
        .map_err(|e| e.to_string())
}

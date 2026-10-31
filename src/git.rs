// Git process invocation; callers interpret successful output.
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{Mutex, OnceLock},
};
pub fn run(dir: &Path, args: &[&str]) -> Result<String, String> {
    String::from_utf8(run_bytes(dir, args)?).map_err(|e| format!("git output: {e}"))
}
pub fn run_bytes(dir: &Path, args: &[&str]) -> Result<Vec<u8>, String> {
    // Cache only directory lookup and pinned history within this process; refs are always read again.
    type Reads = HashMap<(PathBuf, Vec<String>), Vec<u8>>;
    static READS: OnceLock<Mutex<Reads>> = OnceLock::new();
    let reads = READS.get_or_init(Mutex::default);
    let pinned = matches!(args.first(), Some(&"log" | &"rev-list"))
        && args
            .get(1)
            .is_some_and(|s| s.len() == 40 && s.bytes().all(|b| b.is_ascii_hexdigit()))
        && args.iter().skip(2).take_while(|a| **a != "--").all(|a| {
            a.starts_with('-') || (a.len() == 40 && a.bytes().all(|b| b.is_ascii_hexdigit()))
        });
    let immutable = args == ["rev-parse", "--git-common-dir"] || pinned;
    let key = (
        dir.to_path_buf(),
        args.iter().map(|s| s.to_string()).collect(),
    );
    if immutable && let Some(bytes) = reads.lock().map_err(|e| e.to_string())?.get(&key) {
        return Ok(bytes.clone());
    }
    let out = Command::new("git")
        .args(args)
        .current_dir(dir)
        .output()
        .map_err(|e| format!("git: {e}"))?;
    if !out.status.success() {
        // Some failures, such as a merge conflict, are reported on stdout alone.
        let message = if out.stderr.is_empty() {
            &out.stdout
        } else {
            &out.stderr
        };
        return Err(format!("git: {}", String::from_utf8_lossy(message).trim()));
    }
    if immutable {
        let mut reads = reads.lock().map_err(|e| e.to_string())?;
        // Many distinct proof ranges must not retain many copies of a large history.
        if reads.values().map(Vec::len).sum::<usize>() + out.stdout.len() <= 8 * 1024 * 1024 {
            reads.insert(key, out.stdout.clone());
        }
    }
    Ok(out.stdout)
}

fn object_size(reader: &mut impl BufRead, name: &str) -> Result<Option<usize>, String> {
    let mut header = String::new();
    reader.read_line(&mut header).map_err(|e| e.to_string())?;
    if header == format!("{name} missing\n") {
        return Ok(None);
    }
    let mut parts = header.split_whitespace();
    let sha = parts.next().ok_or("missing git object id")?;
    let kind = parts.next().ok_or("missing git object type")?;
    if sha.len() != 40
        || !sha.bytes().all(|b| b.is_ascii_hexdigit())
        || (name.len() == 40 && name.bytes().all(|b| b.is_ascii_hexdigit()) && sha != name)
    {
        return Err(format!("invalid git cat-file response: {header}"));
    }
    let size = parts
        .next()
        .ok_or("missing blob size")?
        .parse()
        .map_err(|e: std::num::ParseIntError| e.to_string())?;
    Ok((kind == "blob").then_some(size))
}
// Read sizes before contents so skipped large blobs never cross the pipe.
pub fn read_objects(
    dir: &Path,
    names: &[String],
    max_bytes: usize,
    mut each: impl FnMut(usize, Option<Vec<u8>>, bool) -> Result<(), String>,
) -> Result<(), String> {
    if names.is_empty() {
        return Ok(());
    }
    let mut child = Command::new("git")
        .args(["cat-file", "--batch-command"])
        .current_dir(dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;
    let mut input = child.stdin.take().ok_or("missing git stdin")?;
    let requests = names
        .iter()
        .map(|name| format!("info {name}\n"))
        .collect::<String>();
    let writer = std::thread::spawn(move || {
        input.write_all(requests.as_bytes())?;
        Ok::<_, std::io::Error>(input)
    });
    let mut reader = BufReader::new(child.stdout.take().ok_or("missing git stdout")?);
    let sizes = names
        .iter()
        .map(|name| object_size(&mut reader, name))
        .collect::<Result<Vec<_>, _>>();
    if sizes.is_err() {
        let _ = child.kill();
    }
    let input = writer.join().map_err(|_| "git input thread failed")?;
    let sizes = match sizes {
        Ok(sizes) => sizes,
        Err(e) => {
            let _ = child.wait();
            return Err(e);
        }
    };
    let mut input = input.map_err(|e| e.to_string())?;
    let requests = names
        .iter()
        .zip(&sizes)
        .filter(|(_, size)| size.is_some_and(|n| n <= max_bytes))
        .map(|(name, _)| format!("contents {name}\n"))
        .collect::<String>();
    let writer = std::thread::spawn(move || input.write_all(requests.as_bytes()));
    let result: Result<(), String> = (|| {
        for (i, (name, size)) in names.iter().zip(sizes).enumerate() {
            let Some(size) = size else {
                each(i, None, false)?;
                continue;
            };
            if size > max_bytes {
                each(i, None, true)?;
                continue;
            }
            if object_size(&mut reader, name)? != Some(size) {
                return Err("git object changed during read".into());
            }
            let mut bytes = vec![0; size];
            reader.read_exact(&mut bytes).map_err(|e| e.to_string())?;
            let mut newline = [0];
            reader.read_exact(&mut newline).map_err(|e| e.to_string())?;
            if newline != *b"\n" {
                return Err("invalid git cat-file separator".into());
            }
            each(i, Some(bytes), true)?;
        }
        Ok(())
    })();
    drop(reader);
    if result.is_err() {
        let _ = child.kill();
    }
    let wrote = writer.join().map_err(|_| "git input thread failed")?;
    let status = child.wait().map_err(|e| e.to_string())?;
    result?;
    wrote.map_err(|e| e.to_string())?;
    if !status.success() {
        return Err("git cat-file failed".into());
    }
    Ok(())
}
pub fn root(dir: &Path) -> Result<std::path::PathBuf, String> {
    let mut bytes = run_bytes(dir, &["rev-parse", "--show-toplevel"])?;
    if bytes.last() == Some(&b'\n') {
        bytes.pop();
    }
    let root =
        String::from_utf8(bytes).map_err(|_| "git repository root is not UTF-8".to_string())?;
    Ok(std::path::PathBuf::from(root))
}

pub fn main_tip(root: &Path) -> Result<String, String> {
    let name = crate::config::main(root)?;
    let local = run(
        root,
        &["rev-parse", "--verify", &format!("refs/heads/{name}")],
    )
    .ok()
    .map(|s| s.trim().to_owned());
    let fallback = || {
        local
            .clone()
            .ok_or_else(|| format!("no local branch {name}"))
    };
    let Some(remote) = crate::config::remote(root)? else {
        return fallback();
    };
    let destination = format!("refs/heads/{name}");
    let tracking = crate::config::destination_ref(&remote, &destination);
    let Ok(cached) = run(root, &["rev-parse", "--verify", &tracking]) else {
        return fallback();
    };
    let cached = cached.trim();
    let Some(local) = local.as_deref() else {
        return Ok(cached.into());
    };
    if local == cached || run(root, &["merge-base", "--is-ancestor", cached, local]).is_ok() {
        return Ok(local.into());
    }
    if run(root, &["merge-base", "--is-ancestor", local, cached]).is_ok() {
        return Ok(cached.into());
    }
    // Divergent local history cannot tell us which destination is current.
    run(
        root,
        &[
            "fetch",
            "--no-tags",
            &remote,
            &format!("+{destination}:{tracking}"),
        ],
    )?;
    run(root, &["rev-parse", "--verify", &tracking]).map(|s| s.trim().to_owned())
}
pub fn local_revision(root: &Path, revision: &str) -> Result<String, String> {
    let end = revision.find(['~', '^']).unwrap_or(revision.len());
    let (name, suffix) = revision.split_at(end);
    if !suffix
        .bytes()
        .all(|b| b.is_ascii_digit() || b == b'~' || b == b'^')
    {
        return Err("gate: expected a local revision".into());
    }
    let base = if name == "main" || name == "refs/heads/main" {
        main_tip(root)?
    } else if name == "HEAD" || (name.len() == 40 && name.bytes().all(|b| b.is_ascii_hexdigit())) {
        name.to_owned()
    } else {
        let branch = name.strip_prefix("refs/heads/").unwrap_or(name);
        let tag = name.strip_prefix("refs/tags/").unwrap_or(name);
        run(
            root,
            &["rev-parse", "--verify", &format!("refs/heads/{branch}")],
        )
        .or_else(|_| {
            run(
                root,
                &["rev-parse", "--verify", &format!("refs/tags/{tag}")],
            )
        })
        .map_err(|_| "gate: expected a local branch, tag or commit".to_string())?
        .trim()
        .to_owned()
    };
    run(
        root,
        &[
            "rev-parse",
            "--verify",
            &format!("{base}{suffix}^{{commit}}"),
        ],
    )
    .map(|s| s.trim().to_owned())
}

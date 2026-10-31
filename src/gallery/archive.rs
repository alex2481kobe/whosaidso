// Monthly media bundles.
mod gzip;
pub mod read;
mod tar;
use super::Media;
use crate::{blob, git, record};
use std::path::Path;
use tar::{archive_size, tar_entry};
pub fn archive_path(root: &Path, nanos: i64) -> Result<std::path::PathBuf, String> {
    Ok(archive_dir(root)?.join(format!("media-{}.tar.gz", month(nanos))))
}
pub fn archive_dir(root: &Path) -> Result<std::path::PathBuf, String> {
    Ok(match std::env::var_os("WHOSAIDSO_ARCHIVE") {
        Some(path) => std::env::current_dir()
            .map_err(|e| e.to_string())?
            .join(path),
        None => root
            .join(git::run(root, &["rev-parse", "--git-common-dir"])?.trim_end_matches('\n'))
            .join("whosaidso/archive"),
    })
}

// Publish complete monthly bundles before prune removes any live record.
pub fn archive(root: &Path, items: &[&Media]) -> Result<(), String> {
    use std::{
        collections::BTreeMap,
        fs,
        io::{Read, Write},
    };
    if items.is_empty() {
        return Ok(());
    }
    let dir = archive_dir(root)?;
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let lock = fs::File::options()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(dir.join(".lock"))
        .map_err(|e| e.to_string())?;
    lock.lock().map_err(|e| e.to_string())?;
    for entry in fs::read_dir(&dir).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        if entry
            .file_name()
            .to_string_lossy()
            .starts_with(".tmp-media-")
            && entry.file_type().map_err(|e| e.to_string())?.is_file()
        {
            fs::remove_file(entry.path()).map_err(|e| e.to_string())?;
        }
    }
    let mut months = BTreeMap::new();
    for item in items {
        months
            .entry(month(item.ran_at))
            .or_insert_with(Vec::new)
            .push(*item);
    }
    for (month, items) in months {
        let target = dir.join(format!("media-{month}.tar.gz"));
        let temp = dir.join(format!(".tmp-media-{month}.tar"));
        let compressed = dir.join(format!(".tmp-media-{month}.tar.gz"));
        let result = (|| -> Result<(), String> {
            let mut output = fs::File::options()
                .write(true)
                .create_new(true)
                .open(&temp)
                .map_err(|e| e.to_string())?;
            match fs::read(&target) {
                Ok(bytes) => {
                    let mut input = std::io::Cursor::new(gzip::decode(&bytes)?);
                    loop {
                        let mut header = [0u8; 512];
                        input.read_exact(&mut header).map_err(|e| e.to_string())?;
                        if header == [0; 512] {
                            break;
                        }
                        let size = archive_size(&header)?;
                        output.write_all(&header).map_err(|e| e.to_string())?;
                        let padded =
                            size.checked_add(511).ok_or("invalid archive size")? / 512 * 512;
                        if std::io::copy(&mut Read::by_ref(&mut input).take(padded), &mut output)
                            .map_err(|e| e.to_string())?
                            != padded
                        {
                            return Err("truncated media archive".into());
                        }
                    }
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
                Err(e) => return Err(e.to_string()),
            }
            for item in items {
                if !blob::verify(root, &item.media_sha256, &item.ext, item.byte_size)? {
                    return Err(format!("cannot archive missing media {}", item.sha));
                }
                let json = fs::read(
                    root.join(".whosaidso/media")
                        .join(format!("{}.json", item.sha)),
                )
                .map_err(|e| e.to_string())?;
                let bytes = fs::read(blob::path(root, &item.media_sha256, &item.ext)?)
                    .map_err(|e| e.to_string())?;
                if record::sha256_hex(&json) != item.sha
                    || record::sha256_hex(&bytes) != item.media_sha256
                    || bytes.len() as u64 != item.byte_size
                {
                    return Err("media changed during archive".into());
                }
                tar_entry(&mut output, &format!("{}.json", item.sha), &json)?;
                tar_entry(&mut output, &format!("{}.{}", item.sha, item.ext), &bytes)?;
            }
            output
                .write_all(&[0; 1024])
                .and_then(|_| output.sync_all())
                .map_err(|e| e.to_string())?;
            drop(output);
            gzip::compress_file(&temp, &compressed)?;
            fs::rename(&compressed, &target).map_err(|e| e.to_string())?;
            fs::remove_file(&temp).map_err(|e| e.to_string())?;
            fs::File::open(&dir)
                .and_then(|f| f.sync_all())
                .map_err(|e| e.to_string())
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temp);
            let _ = fs::remove_file(&compressed);
        }
        result?;
    }
    Ok(())
}
fn month(nanos: i64) -> String {
    // Gregorian civil date from Unix days, including dates before the epoch.
    let days = nanos.div_euclid(1_000_000_000).div_euclid(86400) + 719468;
    let era = days.div_euclid(146097);
    let day = days - era * 146097;
    let year = (day - day / 1460 + day / 36524 - day / 146096) / 365;
    let ordinal = day - (365 * year + year / 4 - year / 100);
    let m = (5 * ordinal + 2) / 153;
    let month = m + if m < 10 { 3 } else { -9 };
    format!("{:04}-{month:02}", year + era * 400 + i64::from(month <= 2))
}

// Merge independently extended monthly bundles without replacing either clone's entries.
pub fn merge_archives(left: &[u8], right: &[u8]) -> Result<Vec<u8>, String> {
    let left = gzip::decode(left)?;
    let right = gzip::decode(right)?;
    let mut entries = std::collections::BTreeMap::new();
    for bytes in [&left, &right] {
        read::items(bytes)?;
        for (name, (size, entry)) in tar::entries(bytes)? {
            if let Some((old_size, old)) = entries.insert(name.to_owned(), (size, entry))
                && (old_size != size || old[512..512 + size] != entry[512..512 + size])
            {
                return Err(format!("conflicting archive entry {name}"));
            }
        }
    }
    let mut output = Vec::new();
    for (_, entry) in entries.values() {
        output.extend_from_slice(entry);
    }
    output.extend_from_slice(&[0; 1024]);
    gzip::encode(&output)
}

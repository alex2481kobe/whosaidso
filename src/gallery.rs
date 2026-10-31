// Media records and commands; HTML rendering belongs in page.rs.
use crate::{blob, git, publish, record, title};
use serde::{Deserialize, Serialize};
use std::path::Path;

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Media {
    #[serde(skip)]
    pub sha: String,
    pub goal: String,
    pub shows: String,
    pub media_sha256: String,
    pub ext: String,
    pub byte_size: u64,
    pub commit: String,
    pub ran_at: i64,
}
pub fn show(root: &Path, cwd: &Path, goal: &str, source: &str, shows: &str) -> Result<(), String> {
    publish::record_dir(root, "media")?;
    let store = record::load(root)?;
    if !store.goals.iter().any(|g| g.id == goal) {
        return Err(format!("unknown goal {goal}"));
    }
    if let Some(reason) = title::title_problem(shows) {
        return Err(format!("bad title: {reason}"));
    }
    let _lock = blob::lock(root, true)?;
    let (media_sha256, ext, byte_size) = blob::put(root, &cwd.join(source))?;
    let commit = git::run(root, &["rev-parse", "HEAD"])?.trim().to_owned();
    let media = Media {
        sha: String::new(),
        goal: goal.into(),
        shows: shows.into(),
        media_sha256,
        ext,
        byte_size,
        commit,
        ran_at: publish::ran_at()?,
    };
    let sha = publish::write_new(&root.join(".whosaidso/media"), &media)?;
    println!("media: {goal} {}", &sha[..12]);
    Ok(())
}

pub const LIVE_MEDIA: usize = 100;
pub fn newest(store: &record::Store) -> Vec<&Media> {
    publish::newest(store.media.iter().map(|m| (m, m.ran_at, m.sha.as_str())))
}
pub struct MediaType {
    pub ext: &'static str,
    pub mime: &'static str,
    pub tag: &'static str,
    signature: fn(&[u8]) -> bool,
}
const TYPES: &[MediaType] = &[
    MediaType {
        ext: "png",
        mime: "image/png",
        tag: "img",
        signature: png,
    },
    MediaType {
        ext: "jpg",
        mime: "image/jpeg",
        tag: "img",
        signature: jpeg,
    },
    MediaType {
        ext: "gif",
        mime: "image/gif",
        tag: "img",
        signature: |b| b.starts_with(b"GIF87a") || b.starts_with(b"GIF89a"),
    },
    MediaType {
        ext: "webp",
        mime: "image/webp",
        tag: "img",
        signature: |b| b.starts_with(b"RIFF") && b.get(8..12) == Some(b"WEBP"),
    },
    MediaType {
        ext: "mov",
        mime: "video/quicktime",
        tag: "video",
        signature: |b| bmff(b).is_some_and(|mut brands| brands.any(|s| s == b"qt  ")),
    },
    MediaType {
        ext: "mp4",
        mime: "video/mp4",
        tag: "video",
        signature: |b| {
            bmff(b).is_some_and(|mut brands| {
                brands.any(|s| {
                    matches!(
                        s,
                        b"isom"
                            | b"iso2"
                            | b"iso3"
                            | b"iso4"
                            | b"iso5"
                            | b"iso6"
                            | b"mp41"
                            | b"mp42"
                            | b"avc1"
                            | b"M4V "
                            | b"MSNV"
                            | b"dash"
                    )
                })
            })
        },
    },
    MediaType {
        ext: "webm",
        mime: "video/webm",
        tag: "video",
        signature: |b| b.starts_with(b"\x1a\x45\xdf\xa3"),
    },
    MediaType {
        ext: "pdf",
        mime: "application/pdf",
        tag: "a",
        signature: |b| b.starts_with(b"%PDF-"),
    },
];
pub fn media_type(ext: &str) -> Result<&'static MediaType, String> {
    TYPES
        .iter()
        .find(|t| t.ext == ext)
        .ok_or_else(|| format!("bad media type {ext}"))
}
pub fn detect(bytes: &[u8]) -> Result<&'static MediaType, String> {
    TYPES.iter().find(|t| (t.signature)(bytes)).ok_or_else(|| "media: unsupported signature and header; supported: PNG, JPEG, GIF, WebP, MP4, MOV, WebM, PDF".into())
}
fn bmff(bytes: &[u8]) -> Option<impl Iterator<Item = &[u8]>> {
    if bytes.get(4..8)? != b"ftyp" {
        return None;
    }
    let size = u32::from_be_bytes(bytes.get(..4)?.try_into().ok()?);
    let (size, start) = match size {
        0 => (bytes.len(), 8),
        1 => (
            usize::try_from(u64::from_be_bytes(bytes.get(8..16)?.try_into().ok()?)).ok()?,
            16,
        ),
        n => (n as usize, 8),
    };
    if size < start + 8 || (size - start) % 4 != 0 {
        return None;
    }
    let brands = bytes.get(start..size)?;
    Some(
        brands
            .chunks_exact(4)
            .enumerate()
            .filter_map(|(i, s)| (i != 1).then_some(s)),
    )
}
fn png(bytes: &[u8]) -> bool {
    bytes.len() >= 33
        && bytes.starts_with(b"\x89PNG\r\n\x1a\n\0\0\0\rIHDR")
        && bytes[16..20] != [0; 4]
        && bytes[20..24] != [0; 4]
        && matches!(
            (bytes[24], bytes[25]),
            (1 | 2 | 4 | 8 | 16, 0) | (8 | 16, 2 | 4 | 6) | (1 | 2 | 4 | 8, 3)
        )
        && bytes[26] == 0
        && bytes[27] == 0
        && bytes[28] <= 1
}
fn jpeg(bytes: &[u8]) -> bool {
    let mut jpeg = false;
    if bytes.starts_with(b"\xff\xd8\xff") {
        let mut at = 2;
        while at + 4 <= bytes.len() && bytes[at] == 255 {
            let marker = bytes[at + 1];
            if marker == 255 {
                at += 1;
                continue;
            }
            let len = u16::from_be_bytes([bytes[at + 2], bytes[at + 3]]) as usize;
            if len < 2 || at + 2 + len > bytes.len() {
                break;
            }
            if matches!(marker, 0xc0..=0xc3 | 0xc5..=0xc7 | 0xc9..=0xcb | 0xcd..=0xcf) {
                let data = &bytes[at + 4..at + 2 + len];
                jpeg = data.len() >= 6
                    && matches!(data[0], 8 | 12 | 16)
                    && data[1..3] != [0; 2]
                    && data[3..5] != [0; 2]
                    && data[5] > 0
                    && data.len() == 6 + 3 * data[5] as usize;
                break;
            }
            if marker == 0xda || marker == 0xd9 {
                break;
            }
            at += 2 + len;
        }
    }
    jpeg
}

pub fn archive_path(root: &Path, nanos: i64) -> Result<std::path::PathBuf, String> {
    Ok(archive_dir(root)?.join(format!("media-{}.tar", month(nanos))))
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
        let target = dir.join(format!("media-{month}.tar"));
        let temp = dir.join(format!(".tmp-media-{month}.tar"));
        let result = (|| -> Result<(), String> {
            let mut output = fs::File::options()
                .write(true)
                .create_new(true)
                .open(&temp)
                .map_err(|e| e.to_string())?;
            match fs::File::open(&target) {
                Ok(mut input) => loop {
                    let mut header = [0u8; 512];
                    input.read_exact(&mut header).map_err(|e| e.to_string())?;
                    if header == [0; 512] {
                        break;
                    }
                    let size = archive_size(&header)?;
                    output.write_all(&header).map_err(|e| e.to_string())?;
                    let padded = size.checked_add(511).ok_or("invalid archive size")? / 512 * 512;
                    if std::io::copy(&mut Read::by_ref(&mut input).take(padded), &mut output)
                        .map_err(|e| e.to_string())?
                        != padded
                    {
                        return Err("truncated media archive".into());
                    }
                },
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
            fs::rename(&temp, &target).map_err(|e| e.to_string())?;
            fs::File::open(&dir)
                .and_then(|f| f.sync_all())
                .map_err(|e| e.to_string())
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temp);
        }
        result?;
    }
    Ok(())
}
fn tar_entry(output: &mut std::fs::File, name: &str, bytes: &[u8]) -> Result<(), String> {
    use std::io::Write;
    let mut header = [0u8; 512];
    header[..name.len()].copy_from_slice(name.as_bytes());
    header[100..108].copy_from_slice(b"0000644\0");
    header[108..124].copy_from_slice(b"0000000\x000000000\0");
    header[124..136].copy_from_slice(format!("{:011o}\0", bytes.len()).as_bytes());
    header[136..148].copy_from_slice(b"00000000000\0");
    header[148..156].fill(b' ');
    header[156] = b'0';
    header[257..265].copy_from_slice(b"ustar\x0000");
    let sum: u64 = header.iter().map(|b| u64::from(*b)).sum();
    header[148..156].copy_from_slice(format!("{sum:06o}\0 ").as_bytes());
    output
        .write_all(&header)
        .and_then(|_| output.write_all(bytes))
        .and_then(|_| output.write_all(&[0; 512][..(512 - bytes.len() % 512) % 512]))
        .map_err(|e| e.to_string())
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
    let mut entries = std::collections::BTreeMap::new();
    for mut bytes in [left, right] {
        while !bytes.is_empty() {
            let header = bytes.get(..512).ok_or("truncated media archive")?;
            if header == [0; 512] {
                if bytes.len() < 1024 || bytes.iter().any(|b| *b != 0) {
                    return Err("invalid media archive end".into());
                }
                break;
            }
            let size = usize::try_from(archive_size(header)?).map_err(|e| e.to_string())?;
            let name = std::str::from_utf8(&header[..100])
                .map_err(|e| e.to_string())?
                .trim_end_matches('\0');
            let (sha, ext) = name.split_once('.').ok_or("invalid archive entry name")?;
            if sha.len() != 64
                || !sha.bytes().all(|b| b.is_ascii_hexdigit())
                || (ext != "json" && media_type(ext).is_err())
            {
                return Err(format!("invalid archive entry {name}"));
            }
            let end = size
                .checked_add(511)
                .and_then(|n| (n / 512).checked_mul(512))
                .and_then(|n| n.checked_add(512))
                .ok_or("invalid archive size")?;
            let entry = bytes.get(..end).ok_or("truncated media archive")?;
            if let Some((old_size, old)) = entries.insert(name.to_owned(), (size, entry))
                && (old_size != size || old[512..512 + size] != entry[512..512 + size])
            {
                return Err(format!("conflicting archive entry {name}"));
            }
            bytes = &bytes[end..];
            if bytes.is_empty() {
                return Err("missing media archive end".into());
            }
        }
    }
    let mut output = Vec::new();
    for (_, entry) in entries.values() {
        output.extend_from_slice(entry);
    }
    output.extend_from_slice(&[0; 1024]);
    Ok(output)
}

fn archive_size(header: &[u8]) -> Result<u64, String> {
    let octal = |b: &[u8]| {
        u64::from_str_radix(
            std::str::from_utf8(b)
                .map_err(|e| e.to_string())?
                .trim_matches(['\0', ' ']),
            8,
        )
        .map_err(|e| e.to_string())
    };
    let sum: u64 = header
        .iter()
        .enumerate()
        .map(|(i, b)| {
            if (148..156).contains(&i) {
                32
            } else {
                u64::from(*b)
            }
        })
        .sum();
    if &header[257..263] != b"ustar\0" || octal(&header[148..156])? != sum || header[156] != b'0' {
        return Err("invalid media archive header".into());
    }
    octal(&header[124..136])
}

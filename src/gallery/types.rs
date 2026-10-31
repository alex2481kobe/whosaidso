// Byte signatures and presentation types.
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
            .as_chunks::<4>()
            .0
            .iter()
            .enumerate()
            .filter_map(|(i, s)| (i != 1).then_some(s.as_slice())),
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

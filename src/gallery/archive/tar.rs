// Tar entries and validated archive merging.
pub(super) fn tar_entry(
    output: &mut std::fs::File,
    name: &str,
    bytes: &[u8],
) -> Result<(), String> {
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

pub(super) fn archive_size(header: &[u8]) -> Result<u64, String> {
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

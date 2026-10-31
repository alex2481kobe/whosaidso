use super::process::command;
use std::{collections::BTreeMap, path::Path};

pub fn entries(bundle: &Path) -> BTreeMap<String, Vec<u8>> {
    let output = command(
        bundle.parent().unwrap(),
        "gzip",
        &["-d", "-c", bundle.to_str().unwrap()],
    )
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
    let mut bytes = output.stdout.as_slice();
    let mut entries = BTreeMap::new();
    loop {
        let (header, rest) = bytes.split_at(512);
        if header == [0; 512] {
            assert!(rest.len() >= 512 && rest.iter().all(|b| *b == 0));
            return entries;
        }
        let name = std::str::from_utf8(&header[..100])
            .unwrap()
            .trim_end_matches('\0');
        let size = usize::from_str_radix(
            std::str::from_utf8(&header[124..136])
                .unwrap()
                .trim_matches(['\0', ' ']),
            8,
        )
        .unwrap();
        assert!(entries.insert(name.into(), rest[..size].to_vec()).is_none());
        bytes = &rest[size.div_ceil(512) * 512..];
    }
}

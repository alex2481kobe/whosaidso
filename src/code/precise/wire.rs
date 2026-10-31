// Bounded protobuf wire decoding.
use super::err;
pub(super) fn malformed() -> String {
    "malformed SCIP index".into()
}

// The wire format and field numbers follow the SCIP protobuf schema (scip.proto).
pub(super) fn varint(data: &[u8], pos: &mut usize) -> Result<u64, String> {
    let mut value = 0u64;
    for i in 0..10 {
        let b = *data.get(*pos).ok_or_else(malformed)?;
        *pos += 1;
        if i == 9 && b > 1 {
            return Err(malformed());
        }
        value |= u64::from(b & 127) << (i * 7);
        if b & 128 == 0 {
            return Ok(value);
        }
    }
    Err(malformed())
}
pub(super) enum Value<'a> {
    Int(u64),
    Bytes(&'a [u8]),
    Other,
}
pub(super) fn fields(data: &[u8]) -> Result<Vec<(u64, Value<'_>)>, String> {
    let mut out = Vec::new();
    let mut p = 0;
    while p < data.len() {
        let tag = varint(data, &mut p)?;
        if tag >> 3 == 0 || tag >> 3 > 0x1fffffff {
            return Err(malformed());
        }
        let v = match tag & 7 {
            0 => Value::Int(varint(data, &mut p)?),
            2 => {
                let n = usize::try_from(varint(data, &mut p)?).map_err(err)?;
                let end = p
                    .checked_add(n)
                    .filter(|&x| x <= data.len())
                    .ok_or_else(malformed)?;
                let b = &data[p..end];
                p = end;
                Value::Bytes(b)
            }
            1 | 5 => {
                let n = if tag & 7 == 1 { 8 } else { 4 };
                p = p
                    .checked_add(n)
                    .filter(|&x| x <= data.len())
                    .ok_or_else(malformed)?;
                Value::Other
            }
            _ => return Err(malformed()),
        };
        out.push((tag >> 3, v));
    }
    Ok(out)
}
pub(super) fn string(v: Value<'_>) -> Result<String, String> {
    match v {
        Value::Bytes(b) => Ok(std::str::from_utf8(b).map_err(|_| malformed())?.to_owned()),
        _ => Err(malformed()),
    }
}
pub(super) fn bytes(v: Value<'_>) -> Result<&[u8], String> {
    if let Value::Bytes(b) = v {
        Ok(b)
    } else {
        Err(malformed())
    }
}
pub(super) fn integer(v: Value<'_>) -> Result<u64, String> {
    if let Value::Int(n) = v {
        Ok(n)
    } else {
        Err(malformed())
    }
}

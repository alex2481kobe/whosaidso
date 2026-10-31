// Source slices preserve the selected snapshot's bytes and the range's position encoding.
use super::{
    Snapshot,
    reads::{Layer, Range, Row, SYNTAX},
};
impl Snapshot {
    pub fn select(&self, value: &str) -> Result<Vec<Row>, String> {
        if value.starts_with(&format!("{}:", self.project)) {
            let paths = self.identifier_paths(value)?;
            return Ok(self
                .definitions_in(&paths)?
                .rows
                .into_iter()
                .filter(|r| r.symbol == value)
                .collect());
        }
        let (path, name) = value
            .split_once("::")
            .filter(|(p, _)| self.files.entries.iter().any(|f| f.path == *p))
            .map_or((None, value), |(p, n)| (Some(p), n));
        Ok(self
            .definitions(name)?
            .rows
            .into_iter()
            .filter(|r| path.is_none_or(|p| r.path == p))
            .collect())
    }
    fn identifier_paths(&self, value: &str) -> Result<Vec<String>, String> {
        let (sql, key) = if let Some(raw) =
            value.strip_prefix(&format!("{}:{SYNTAX}:", self.project))
        {
            let Some((blob, _)) = raw.split_once(':') else {
                return Ok(vec![]);
            };
            (
                "SELECT m.path FROM facts f JOIN manifest_files m ON m.fact=f.id WHERE f.blob=?1 AND m.manifest=(SELECT manifest FROM selection)",
                blob,
            )
        } else {
            let prefix = self.precise_prefix()?;
            let Some(symbol) = value.strip_prefix(&prefix) else {
                return Ok(vec![]);
            };
            (
                "SELECT DISTINCT path FROM scip_occ WHERE symbol=?1 AND is_def=1",
                symbol,
            )
        };
        let mut stmt = self.db.prepare(sql).map_err(super::err)?;
        stmt.query_map([key], |r| r.get(0))
            .map_err(super::err)?
            .collect::<Result<_, _>>()
            .map_err(super::err)
    }
    pub fn outline(&self, path: &str) -> Result<Vec<Row>, String> {
        Ok(self.definitions_in(&[path.to_owned()])?.rows)
    }
    pub fn symbol_body(&self, row: &Row) -> Result<Option<(Range, Vec<u8>)>, String> {
        let range = match row.layer {
            Layer::Precise => match &row.enclosing {
                Some(r) => r,
                None => return Ok(None),
            },
            Layer::Syntactic => &row.range,
        };
        let Some(body) = self.body(&row.path)? else {
            return Ok(None);
        };
        let start = offset(&body, range.line, range.start, range.encoding)?;
        let end = offset(&body, range.end_line, range.stop, range.encoding)?;
        let bytes = body.get(start..end).ok_or("invalid symbol range")?.to_vec();
        Ok(Some((range.clone(), bytes)))
    }
}
fn offset(bytes: &[u8], line: i64, column: Option<i64>, encoding: i64) -> Result<usize, String> {
    let mut start = 0;
    for _ in 1..line {
        start += bytes
            .get(start..)
            .and_then(|s| s.iter().position(|b| *b == b'\n'))
            .ok_or("invalid symbol line")?
            + 1;
    }
    let text = std::str::from_utf8(&bytes[start..]).map_err(|e| e.to_string())?;
    let column = column.ok_or("unknown symbol columns")? as usize;
    if encoding <= 1 {
        return Ok(start + column);
    }
    let mut units = 0;
    for (byte, c) in text.char_indices() {
        if units == column {
            return Ok(start + byte);
        }
        units += if encoding == 2 { c.len_utf16() } else { 1 };
    }
    if units == column {
        Ok(bytes.len())
    } else {
        Err("invalid symbol column".into())
    }
}

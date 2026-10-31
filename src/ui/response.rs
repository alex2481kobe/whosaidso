// HTTP reply encoding and byte range responses.
use std::{io::Write, net::TcpStream};
pub(super) struct Reply {
    pub(super) code: u16,
    pub(super) mime: &'static str,
    pub(super) bytes: Vec<u8>,
    pub(super) headers: Vec<(String, String)>,
}
impl Reply {
    pub(super) fn bytes(mime: &'static str, bytes: &[u8]) -> Self {
        Self {
            code: 200,
            mime,
            bytes: bytes.into(),
            headers: Vec::new(),
        }
    }
    pub(super) fn error(code: u16, message: &str) -> Self {
        let mut reply = Self::bytes(
            "application/json; charset=utf-8",
            serde_json::json!({"error":{"message":message}})
                .to_string()
                .as_bytes(),
        );
        reply.code = code;
        reply
    }
    pub(super) fn json(value: &serde_json::Value) -> Self {
        Self::bytes(
            "application/json; charset=utf-8",
            value.to_string().as_bytes(),
        )
    }
    pub(super) fn header(&mut self, key: &str, value: impl Into<String>) {
        self.headers.push((key.into(), value.into()));
    }
}
pub(super) fn apply_range(reply: &mut Reply, range: &str) {
    let size = reply.bytes.len();
    let bounds = range
        .strip_prefix("bytes=")
        .and_then(|r| r.split_once('-'))
        .and_then(|(a, b)| {
            if a.is_empty() {
                let n: usize = b.parse().ok()?;
                (n > 0).then_some((size.saturating_sub(n), size.saturating_sub(1)))
            } else {
                Some((
                    a.parse::<usize>().ok()?,
                    if b.is_empty() {
                        size.saturating_sub(1)
                    } else {
                        b.parse::<usize>().ok()?.min(size.saturating_sub(1))
                    },
                ))
            }
        });
    if let Some((start, end)) = bounds.filter(|&(a, b)| a <= b && b < size) {
        reply.bytes = reply.bytes[start..=end].to_vec();
        reply.code = 206;
        reply.header("Content-Range", format!("bytes {start}-{end}/{size}"));
    } else {
        reply.bytes.clear();
        reply.code = 416;
        reply.header("Content-Range", format!("bytes */{size}"));
    }
}
pub(super) fn write_reply(stream: &mut TcpStream, reply: Reply) -> std::io::Result<()> {
    let reason = match reply.code {
        200 => "OK",
        206 => "Partial Content",
        303 => "See Other",
        400 => "Bad Request",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        416 => "Range Not Satisfiable",
        _ => "Internal Server Error",
    };
    write!(
        stream,
        "HTTP/1.1 {} {reason}\r\nContent-Type: {}\r\nContent-Length: {}\r\nConnection: close\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nX-Content-Type-Options: nosniff\r\nContent-Security-Policy: default-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'\r\n",
        reply.code,
        reply.mime,
        reply.bytes.len()
    )?;
    for (key, value) in reply.headers {
        write!(stream, "{key}: {value}\r\n")?;
    }
    stream.write_all(b"\r\n")?;
    stream.write_all(&reply.bytes)
}

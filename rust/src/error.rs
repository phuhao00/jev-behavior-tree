use std::fmt;

#[derive(Debug)]
pub struct IntuitionError {
    pub status: u16,
    pub message: String,
}

impl fmt::Display for IntuitionError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for IntuitionError {}

pub fn err_bad(status: u16, message: impl Into<String>) -> IntuitionError {
    IntuitionError {
        status,
        message: message.into(),
    }
}

pub fn redact(message: &str) -> String {
    let mut out = String::new();
    let bytes = message.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i..].starts_with(b"vck_") {
            let mut j = i + 4;
            while j < bytes.len()
                && (bytes[j].is_ascii_alphanumeric() || bytes[j] == b'_' || bytes[j] == b'-')
            {
                j += 1;
            }
            out.push_str("[redacted]");
            i = j;
            continue;
        }
        let rest = &message[i..];
        if rest.len() >= 7 && rest[..7].eq_ignore_ascii_case("bearer ") {
            out.push_str("Bearer [redacted]");
            i += 7;
            while i < bytes.len() && !bytes[i].is_ascii_whitespace() {
                i += 1;
            }
            continue;
        }
        let ch = rest.chars().next().unwrap();
        out.push(ch);
        i += ch.len_utf8();
    }
    let chars: Vec<char> = out.chars().collect();
    if chars.len() <= 500 {
        out
    } else {
        chars[..500].iter().collect()
    }
}

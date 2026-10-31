// Shared code-query names and bounded answers.
#[derive(Default)]
pub struct Answers {
    pub lines: Vec<String>,
    pub total: usize,
}
impl Answers {
    pub fn push(&mut self, line: impl FnOnce() -> String) {
        self.total += 1;
        if self.lines.len() < 20 {
            self.lines.push(crate::text::cut(&line(), 200));
        }
    }
}
pub fn parts(value: &str) -> Vec<&str> {
    value.split("::").flat_map(|s| s.split('.')).collect()
}
pub fn matches(qualified: &str, value: &str) -> bool {
    parts(qualified).ends_with(&parts(value))
}
pub fn leaf(value: &str) -> &str {
    value
        .rsplit("::")
        .next()
        .unwrap_or(value)
        .rsplit('.')
        .next()
        .unwrap_or(value)
}

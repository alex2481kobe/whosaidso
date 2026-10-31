// Shared code-query names and output budgets.
pub(crate) const DEFAULT_LIMIT: usize = 20;
pub(crate) const MAX_LIMIT: usize = 100;
pub(crate) const GAP_LIMIT: usize = 8;
pub(crate) const ROW_BYTES: usize = 500;
#[derive(Default)]
pub struct Answers {
    pub lines: Vec<String>,
    pub total: usize,
}
impl Answers {
    pub fn push(&mut self, line: impl FnOnce() -> String) {
        self.total += 1;
        if self.lines.len() < DEFAULT_LIMIT {
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

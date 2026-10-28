// Image titles are nonempty and bounded for display.
pub fn title_problem(shows: &str) -> Option<String> {
    if shows.trim().is_empty() || shows.chars().count() > 80 { Some("length must be 1 to 80 nonempty characters".into()) } else { None }
}

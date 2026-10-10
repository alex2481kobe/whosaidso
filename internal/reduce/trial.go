package reduce

// Every write the fold makes to state goes through put, here, so one place
// sees each change an event makes.

// put writes m[k] = v.
func put[K comparable, V any](s *state, m map[K]V, k K, v V) {
	m[k] = v
}

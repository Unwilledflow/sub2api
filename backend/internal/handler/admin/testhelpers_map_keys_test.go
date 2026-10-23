package admin

// mapKeys returns the keys of values, used by the simple-mode shape tests to
// assert that a response payload exposes exactly an allowlist of fields.
func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
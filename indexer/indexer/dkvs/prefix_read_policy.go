package dkvs

// validateReadablePrefix limits stateless aggregate reads to the same
// namespace shapes exposed by the application protocol. It grants no managed
// synchronization semantics and does not create server-side state.
func validateReadablePrefix(prefix string) error {
	parsed, err := ParsePrefix(prefix)
	if err != nil {
		return err
	}
	segments := parsed.Segments
	switch parsed.Namespace {
	case "account":
		if (len(segments) == 1 || len(segments) == 2) && validAccountNetwork(segments[0]) {
			return nil
		}
	case "personal":
		if (len(segments) == 1 || len(segments) >= 2) && validAccountID(segments[0]) {
			return nil
		}
	case "mail":
		if len(segments) >= 1 && validAccountID(segments[0]) {
			return nil
		}
	case "blob":
		if (len(segments) == 1 || len(segments) == 2) && validAccountID(segments[0]) {
			return nil
		}
	case "svc", "sys":
		if len(segments) >= 1 {
			return nil
		}
	case "name", "tmp":
		if len(segments) == 1 {
			return nil
		}
	}
	return ErrInvalidKey
}

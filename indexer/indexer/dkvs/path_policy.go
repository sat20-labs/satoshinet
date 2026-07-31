package dkvs

import "strings"

func collectionPath(parsed ParsedKey) string {
	if len(parsed.Segments) == 0 {
		return ""
	}
	switch parsed.Namespace {
	case "account":
		if len(parsed.Segments) == 2 {
			return "/account/" + parsed.Segments[0] + "/" + parsed.Segments[1]
		}
	case "personal":
		if len(parsed.Segments) >= 2 {
			return "/personal/" + parsed.Segments[0] + "/" + parsed.Segments[1]
		}
	case "name":
		if len(parsed.Segments) == 1 {
			return "/name/" + parsed.Segments[0]
		}
	case "svc":
		if len(parsed.Segments) >= 1 {
			return "/svc/" + parsed.Segments[0]
		}
	case "mail":
		if len(parsed.Segments) >= 3 && parsed.Segments[1] == "msg" {
			return "/mail/" + parsed.Segments[0] + "/msg/" + parsed.Segments[2]
		}
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "share" {
			return "/mail/" + parsed.Segments[0] + "/share"
		}
	case "blob":
		if len(parsed.Segments) == 2 {
			return "/blob/" + parsed.Segments[0] + "/" + parsed.Segments[1]
		}
	case "tmp":
		if len(parsed.Segments) == 1 {
			return "/tmp/" + parsed.Segments[0]
		}
	case "sys":
		if len(parsed.Segments) >= 1 {
			return "/sys/" + parsed.Segments[0]
		}
	}
	return ""
}

func pathMode(parsed ParsedKey) PathMode {
	switch parsed.Namespace {
	case "personal", "blob":
		return PathOwnerExclusive
	case "mail":
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "msg" {
			return PathSharedAppend
		}
		return PathOwnerExclusive
	case "tmp":
		return PathLocalOnly
	case "account", "name", "svc", "sys":
		return PathAuthorityExclusive
	default:
		return PathAuthorityExclusive
	}
}

func collectionPathForPrefix(prefix string) string {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	parsed, err := ParsePrefix(prefix)
	if err != nil {
		return ""
	}
	switch parsed.Namespace {
	case "account":
		if len(parsed.Segments) == 2 {
			return prefix
		}
	case "personal":
		// /personal/<account> is retained as a read-only aggregate query for
		// existing directory/usage callers. It is never a write/CAS path.
		if (len(parsed.Segments) == 1 || len(parsed.Segments) == 2) && validAccountID(parsed.Segments[0]) {
			return prefix
		}
	case "name":
		if len(parsed.Segments) == 1 {
			return prefix
		}
	case "svc":
		if len(parsed.Segments) == 1 {
			return prefix
		}
	case "mail":
		if len(parsed.Segments) == 3 && parsed.Segments[1] == "msg" && validAccountID(parsed.Segments[2]) {
			return prefix
		}
		if len(parsed.Segments) == 2 && parsed.Segments[1] == "share" {
			return prefix
		}
	case "blob":
		if len(parsed.Segments) == 2 && validAccountID(parsed.Segments[0]) {
			return prefix
		}
	case "tmp":
		if len(parsed.Segments) == 1 {
			return prefix
		}
	case "sys":
		if len(parsed.Segments) == 1 {
			return prefix
		}
	}
	return ""
}

func isCanonicalCollectionPath(path string) bool {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	parsed, err := ParsePrefix(path)
	return err == nil && collectionPath(parsed) == path
}

func CollectionPathForKey(key string) (string, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return "", err
	}
	path := collectionPath(parsed)
	if path == "" {
		return "", ErrInvalidKey
	}
	return path, nil
}

func PathModeForKey(key string) (PathMode, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return 0, err
	}
	return pathMode(parsed), nil
}

func PathModeForRecord(record *Record) (PathMode, error) {
	if record == nil {
		return 0, ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return 0, err
	}
	if isFreeLocalRecord(record) {
		return PathLocalOnly, nil
	}
	return pathMode(parsed), nil
}

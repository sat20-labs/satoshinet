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
		if len(parsed.Segments) >= 2 {
			// A mailbox is one endpoint-managed prefix. Any direct message,
			// topic delivery, key delivery or share changes the same PathMeta
			// endpoint generation, so a wallet never needs to know sender/topic
			// children before it can synchronize its mailbox.
			return "/mail/" + parsed.Segments[0]
		}
	case "topic":
		if len(parsed.Segments) >= 2 {
			return "/topic/" + parsed.Segments[0]
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
		// AccountBound controls replication placement, not prefix semantics.
		// Mailboxes still have ordinary endpoint PathMeta/snapshot behavior.
		return PathAuthorityExclusive
	case "tmp":
		return PathLocalOnly
	case "account", "name", "svc", "sys", "topic":
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
		if len(parsed.Segments) == 1 && validAccountID(parsed.Segments[0]) {
			return prefix
		}
	case "topic":
		if len(parsed.Segments) == 1 {
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
	if err != nil || collectionPathForPrefix(path) != path {
		return false
	}
	// These two one-segment forms are read-only aggregates. Their canonical
	// collection paths contain the second segment selected by each record.
	if (parsed.Namespace == "personal" || parsed.Namespace == "blob") && len(parsed.Segments) == 1 {
		return false
	}
	return true
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

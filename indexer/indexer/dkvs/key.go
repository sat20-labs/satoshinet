package dkvs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type ParsedKey struct {
	Namespace string
	Segments  []string
}

var allowedNamespaces = map[string]struct{}{
	"sys":      {},
	"name":     {},
	"svc":      {},
	"account":  {},
	"personal": {},
	"mail":     {},
	"topic":    {},
	"blob":     {},
	"tmp":      {},
}

func ParseKey(key string) (ParsedKey, error) {
	parsed, err := parseKeyParts(key)
	if err != nil {
		return parsed, err
	}
	return parsed, validateNamespaceShape(parsed)
}

func ParsePrefix(prefix string) (ParsedKey, error) {
	return parseKeyParts(strings.TrimSuffix(prefix, "/"))
}

func parseKeyParts(key string) (ParsedKey, error) {
	var parsed ParsedKey
	if len(key) == 0 || len(key) > MaxKeySize || key[0] != '/' {
		return parsed, ErrInvalidKey
	}
	if strings.Contains(key, "//") {
		return parsed, ErrInvalidKey
	}
	parts := strings.Split(strings.TrimPrefix(key, "/"), "/")
	if len(parts) < 2 {
		return parsed, ErrInvalidKey
	}
	ns := parts[0]
	if len(ns) == 0 || len(ns) > MaxNamespaceSize {
		return parsed, ErrInvalidNamespace
	}
	if _, ok := allowedNamespaces[ns]; !ok {
		return parsed, ErrInvalidNamespace
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > MaxKeySegmentSize || !validSegment(part) {
			return parsed, ErrInvalidKey
		}
	}
	parsed.Namespace = ns
	parsed.Segments = parts[1:]
	return parsed, nil
}

func validSegment(segment string) bool {
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// NormalizeNameID provides a reversible presentation normalization for DKVS
// identifiers: trim leading/trailing whitespace and convert ASCII whitespace
// runs to a single underscore. It never hashes or otherwise invents an ID.
// Any remaining character invalid for a DKVS segment is rejected later by
// ParseKey.
func NormalizeNameID(canonicalName string) string {
	canonicalName = strings.ToLower(strings.TrimSpace(canonicalName))
	var out strings.Builder
	underscore := false
	for i := 0; i < len(canonicalName); i++ {
		c := canonicalName[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			if out.Len() != 0 && !underscore {
				out.WriteByte('_')
				underscore = true
			}
			continue
		}
		out.WriteByte(c)
		underscore = c == '_'
	}
	return strings.Trim(out.String(), "_")
}

func validAccountID(accountID string) bool {
	if len(accountID) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(accountID)
	return err == nil
}

func IsTombstone(flags uint32) bool {
	return flags&FlagTombstone != 0
}

func validateNamespaceShape(parsed ParsedKey) error {
	switch parsed.Namespace {
	case "account":
		if len(parsed.Segments) != 2 || !validAccountNetwork(parsed.Segments[0]) {
			return ErrInvalidKey
		}
	case "personal":
		if len(parsed.Segments) < 2 || !validAccountID(parsed.Segments[0]) {
			return ErrInvalidKey
		}
	case "name":
		if len(parsed.Segments) != 1 {
			return ErrInvalidKey
		}
	case "svc":
		if len(parsed.Segments) < 2 {
			return ErrInvalidKey
		}
	case "mail":
		if len(parsed.Segments) == 0 || !validAccountID(parsed.Segments[0]) {
			return ErrInvalidKey
		}
		// /mail/<recipient>/msg/<sender>/<message_id>
		if len(parsed.Segments) == 4 && parsed.Segments[1] == "msg" && validAccountID(parsed.Segments[2]) {
			return nil
		}
		// /mail/<recipient>/share/<package>/<share>
		if len(parsed.Segments) == 4 && parsed.Segments[1] == "share" {
			return nil
		}
		// /mail/<recipient>/topic/<topic>/msg/<sender>/<message_id>
		if len(parsed.Segments) == 6 && parsed.Segments[1] == "topic" &&
			parsed.Segments[3] == "msg" && validAccountID(parsed.Segments[4]) {
			return nil
		}
		// /mail/<recipient>/topic/<topic>/key/<key_seq>
		if len(parsed.Segments) == 5 && parsed.Segments[1] == "topic" && parsed.Segments[3] == "key" {
			return nil
		}
		return ErrInvalidKey
	case "topic":
		// /topic/<topic>/meta
		// /topic/<topic>/state
		if len(parsed.Segments) == 2 && (parsed.Segments[1] == "meta" || parsed.Segments[1] == "state") {
			return nil
		}
		// /topic/<topic>/members/<account_id>
		if len(parsed.Segments) == 3 && parsed.Segments[1] == "members" && validAccountID(parsed.Segments[2]) {
			return nil
		}
		return ErrInvalidKey
	case "blob":
		if len(parsed.Segments) != 2 || !validAccountID(parsed.Segments[0]) {
			return ErrInvalidKey
		}
	case "tmp":
		if len(parsed.Segments) != 1 {
			return ErrInvalidKey
		}
	case "sys":
		switch parsed.Segments[0] {
		case "params":
			if len(parsed.Segments) == 1 {
				return nil
			}
		case "checkpoint", "snapshot", "miner", "pool":
			if len(parsed.Segments) == 2 {
				return nil
			}
		}
		return ErrInvalidKey
	}
	return nil
}

package dkvs

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
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
	"personal": {},
	"mail":     {},
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

func NormalizeNameID(canonicalName string) string {
	if len(canonicalName) > 0 && len(canonicalName) <= MaxKeySegmentSize && validSegment(canonicalName) {
		return canonicalName
	}
	sum := sha256.Sum256([]byte(canonicalName))
	return hex.EncodeToString(sum[:])
}

func personalAccountID(pubKey []byte) string {
	sum := sha256.Sum256(pubKey)
	return hex.EncodeToString(sum[:])
}

func IsTombstone(flags uint32) bool {
	return flags&FlagTombstone != 0
}

func validateNamespaceShape(parsed ParsedKey) error {
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || len(parsed.Segments[0]) != sha256.Size*2 {
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
		if len(parsed.Segments) == 3 && parsed.Segments[1] == "msg" {
			return nil
		}
		if len(parsed.Segments) == 4 && parsed.Segments[1] == "share" {
			return nil
		}
		return ErrInvalidKey
	case "blob":
		if len(parsed.Segments) == 2 && parsed.Segments[1] == "manifest" {
			return nil
		}
		if len(parsed.Segments) == 3 && parsed.Segments[1] == "chunk" {
			if index, err := strconv.Atoi(parsed.Segments[2]); err != nil || index < 0 {
				return ErrInvalidKey
			}
			return nil
		}
		return ErrInvalidKey
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

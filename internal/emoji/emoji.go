package emoji

import (
	"fmt"
	"strings"
)

const (
	emojiVariationText  = rune(0xFE0E)
	emojiVariationImage = rune(0xFE0F)
	emojiModifierMin    = rune(0x1F3FB)
	emojiModifierMax    = rune(0x1F3FF)
	emojiKeycap         = rune(0x20E3)
	emojiZWJ            = rune(0x200D)
)

// AssetID returns a provider-neutral ID for a standard emoji grapheme cluster.
// The ID is a lowercase hyphen-separated codepoint sequence with emoji
// presentation selectors removed.
func AssetID(cluster string) (string, bool) {
	runes := []rune(cluster)
	if !isStandardCluster(runes) {
		return "", false
	}

	parts := make([]string, 0, len(runes))
	for _, r := range runes {
		if isVariationSelector(r) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "-"), true
}

// IsCluster reports whether cluster is a standard emoji grapheme cluster.
func IsCluster(cluster string) bool {
	_, ok := AssetID(cluster)
	return ok
}

func isStandardCluster(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	if isKeycapCluster(runes) {
		return true
	}
	if isRegionalIndicatorFlag(runes) {
		return true
	}

	seenBase := false
	expectBase := true
	for _, r := range runes {
		switch {
		case isBase(r):
			if !expectBase {
				return false
			}
			seenBase = true
			expectBase = false
		case isModifier(r):
			if expectBase || !seenBase {
				return false
			}
		case isVariationSelector(r):
		case r == emojiZWJ:
			if expectBase || !seenBase {
				return false
			}
			expectBase = true
		default:
			return false
		}
	}
	return seenBase && !expectBase
}

func isKeycapCluster(runes []rune) bool {
	if len(runes) != 2 && len(runes) != 3 {
		return false
	}
	if !isKeycapBase(runes[0]) {
		return false
	}
	if len(runes) == 2 {
		return runes[1] == emojiKeycap
	}
	return runes[1] == emojiVariationImage && runes[2] == emojiKeycap
}

func isRegionalIndicatorFlag(runes []rune) bool {
	if len(runes) != 2 {
		return false
	}
	return isRegionalIndicator(runes[0]) && isRegionalIndicator(runes[1])
}

func isBase(r rune) bool {
	if isModifier(r) || isRegionalIndicator(r) {
		return false
	}
	switch {
	case r == 0x00A9 || r == 0x00AE || r == 0x203C || r == 0x2049 ||
		r == 0x2122 || r == 0x2139 || r == 0x2328 || r == 0x23CF ||
		r == 0x24C2 || r == 0x25B6 || r == 0x25C0 || r == 0x3030 ||
		r == 0x303D || r == 0x3297 || r == 0x3299:
		return true
	case r >= 0x2194 && r <= 0x2199:
		return true
	case r >= 0x21A9 && r <= 0x21AA:
		return true
	case r >= 0x231A && r <= 0x231B:
		return true
	case r >= 0x23E9 && r <= 0x23F3:
		return true
	case r >= 0x23F8 && r <= 0x23FA:
		return true
	case r >= 0x25AA && r <= 0x25AB:
		return true
	case r >= 0x25FB && r <= 0x25FE:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x2B05 && r <= 0x2B07:
		return true
	case r >= 0x2B1B && r <= 0x2B1C:
		return true
	case r == 0x2B50 || r == 0x2B55:
		return true
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	default:
		return false
	}
}

func isKeycapBase(r rune) bool {
	return r == '#' || r == '*' || (r >= '0' && r <= '9')
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

func isModifier(r rune) bool {
	return r >= emojiModifierMin && r <= emojiModifierMax
}

func isVariationSelector(r rune) bool {
	return r == emojiVariationText || r == emojiVariationImage
}

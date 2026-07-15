package validation

import (
	"net/netip"
	"strings"
)

// EmailASCIIValid validates the conservative ASCII email shape used by the scanner.
func EmailASCIIValid(candidate string) bool {
	s := strings.TrimSpace(candidate)
	if s == "" || len(s) > 254 {
		return false
	}

	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || domain == "" {
		return false
	}
	if strings.Contains(local, "..") || local[0] == '.' || local[len(local)-1] == '.' {
		return false
	}
	if len(local) > 64 || len(domain) > 253 {
		return false
	}

	for _, part := range strings.Split(local, ".") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !isASCIILocalPartChar(c) {
				return false
			}
		}
	}

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for i, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !isASCIIDomainChar(c) {
				return false
			}
		}
		if i == len(labels)-1 && !isAlpha(label) {
			return false
		}
	}

	return true
}

// PaymentCardValid validates a PAN shape already reduced to digits, including
// the Luhn checksum.
func PaymentCardValid(digits string) bool {
	return PaymentCardShape(digits) && LuhnValid(digits)
}

// PaymentCardShape validates a PAN brand and length WITHOUT the Luhn checksum.
// Used by the keyword-gated card fallback to catch typo'd/synthetic cards that
// carry a valid brand shape but a broken checksum.
func PaymentCardShape(digits string) bool {
	if !allDigits(digits) || len(digits) < 13 || len(digits) > 19 {
		return false
	}

	switch {
	case hasPrefixDigits(digits, "4"):
		return len(digits) == 13 || len(digits) == 16 || len(digits) == 19
	case hasPrefixDigits(digits, "34"), hasPrefixDigits(digits, "37"):
		return len(digits) == 15
	case prefixInRange(digits, 2, 51, 55), prefixInRange(digits, 4, 2221, 2720):
		return len(digits) == 16
	case hasPrefixDigits(digits, "6011"), prefixInRange(digits, 3, 644, 649), hasPrefixDigits(digits, "65"):
		return len(digits) >= 16 && len(digits) <= 19
	case hasPrefixDigits(digits, "62"):
		return len(digits) >= 16 && len(digits) <= 19
	case prefixInRange(digits, 4, 2200, 2204):
		return len(digits) >= 16 && len(digits) <= 19
	default:
		return false
	}
}

func parseIPCandidate(candidate string) (netip.Addr, bool) {
	s := strings.TrimSpace(candidate)
	if s == "" {
		return netip.Addr{}, false
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr().Unmap(), true
	}
	s = strings.Trim(s, "[]")
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isPrivateOrLocal(addr netip.Addr) bool {
	return addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsUnspecified()
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hasPrefixDigits(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

func prefixInRange(s string, prefixLen int, min, max int) bool {
	if len(s) < prefixLen {
		return false
	}
	n := 0
	for i := range prefixLen {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= min && n <= max
}

func isASCIILocalPartChar(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	}

	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '/', '=', '?', '^', '_', '`', '{', '|', '}', '~':
		return true
	default:
		return false
	}
}

func isASCIIDomainChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-'
}

func isAlpha(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return s != ""
}

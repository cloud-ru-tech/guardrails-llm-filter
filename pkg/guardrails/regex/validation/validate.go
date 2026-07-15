package validation

import (
	"strings"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Validate reports whether candidate satisfies all validators declared by rl.
func Validate(candidate string, rl rule.Rule) bool {
	digits := stripNonDigits(candidate)

	for _, validator := range rl.Validators {
		switch validator {
		case rule.ValidatorLuhn:
			if !LuhnValid(digits) {
				return false
			}
		case rule.ValidatorSNILS:
			if !SNILSValid(digits) {
				return false
			}
		case rule.ValidatorINNPerson:
			if !INNPersonValid(digits) {
				return false
			}
		case rule.ValidatorINNOrg:
			if !INNOrgValid(digits) {
				return false
			}
		case rule.ValidatorOGRN:
			if !OGRNValid(digits) {
				return false
			}
		case rule.ValidatorOGRNIP:
			if !OGRNIPValid(digits) {
				return false
			}
		case rule.ValidatorIBANMod97:
			if !IBANMod97Valid(candidate) {
				return false
			}
		case rule.ValidatorEmailASCII:
			if !EmailASCIIValid(candidate) {
				return false
			}
		case rule.ValidatorPaymentCard:
			if !PaymentCardValid(digits) {
				return false
			}
		case rule.ValidatorPaymentCardNoLuhn:
			if !PaymentCardShape(digits) {
				return false
			}
		case rule.ValidatorEntropy:
			if rl.Entropy > 0 && ShannonEntropy(candidate) < rl.Entropy {
				return false
			}
		case rule.ValidatorBanlist:
			if banlisted(candidate, rl.Banlist) {
				return false
			}
		case rule.ValidatorIPv4:
			addr, ok := parseIPCandidate(candidate)
			if !ok || !addr.Is4() {
				return false
			}
		case rule.ValidatorIPv6:
			addr, ok := parseIPCandidate(candidate)
			if !ok || !addr.Is6() {
				return false
			}
		case rule.ValidatorIPPublic:
			addr, ok := parseIPCandidate(candidate)
			if !ok || isPrivateOrLocal(addr) {
				return false
			}
		case rule.ValidatorIPPrivate:
			addr, ok := parseIPCandidate(candidate)
			if !ok || !isPrivateOrLocal(addr) {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// IsKnown reports whether validator is supported by the runtime validation package.
func IsKnown(validator rule.ValidatorType) bool {
	switch validator {
	case rule.ValidatorLuhn,
		rule.ValidatorSNILS,
		rule.ValidatorINNPerson,
		rule.ValidatorINNOrg,
		rule.ValidatorOGRN,
		rule.ValidatorOGRNIP,
		rule.ValidatorIBANMod97,
		rule.ValidatorEmailASCII,
		rule.ValidatorPaymentCard,
		rule.ValidatorPaymentCardNoLuhn,
		rule.ValidatorEntropy,
		rule.ValidatorBanlist,
		rule.ValidatorIPv4,
		rule.ValidatorIPv6,
		rule.ValidatorIPPublic,
		rule.ValidatorIPPrivate:
		return true
	default:
		return false
	}
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func banlisted(candidate string, banlist []string) bool {
	lower := strings.ToLower(candidate)
	for _, banned := range banlist {
		if lower == banned {
			return true
		}
	}
	return false
}

package validation

import (
	"strconv"
	"strings"
	"unicode"
)

// mod97DecimalString computes the remainder of a decimal digit string modulo 97.
// s must contain only ASCII digit characters '0' to '9'.
func mod97DecimalString(s string) int {
	remainder := 0
	for _, c := range s {
		remainder = (remainder*10 + int(c-'0')) % 97
	}
	return remainder
}

// LuhnSum returns the Luhn digit-sum for the given string of digits.
// digits must contain only ASCII digit characters.
func LuhnSum(digits string) int {
	sum := 0
	n := len(digits)
	parity := n % 2
	for i, ch := range digits {
		d := int(ch - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum
}

// LuhnValid reports whether digits pass the Luhn algorithm.
func LuhnValid(digits string) bool {
	if len(digits) == 0 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return LuhnSum(digits)%10 == 0
}

// LuhnCheckDigit returns the Luhn check digit for the given prefix digits.
func LuhnCheckDigit(prefix string) rune {
	sum := 0
	n := len(prefix)
	parity := (n + 1) % 2
	for i, ch := range prefix {
		d := int(ch - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	check := (10 - (sum % 10)) % 10
	return rune('0' + check)
}

// SNILSChecksum computes the 2-digit checksum for a 9-digit SNILS prefix.
func SNILSChecksum(nineDigits string) string {
	if len(nineDigits) != 9 {
		return "00"
	}
	sum := 0
	for i, ch := range nineDigits {
		sum += int(ch-'0') * (9 - i)
	}
	checksum := sum % 101
	if checksum > 99 {
		checksum = 0
	}
	return string([]byte{byte('0' + checksum/10), byte('0' + checksum%10)})
}

// SNILSValid validates a SNILS number reduced to digits.
func SNILSValid(digits string) bool {
	if len(digits) != 11 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	checksum := SNILSChecksum(digits[:9])
	return digits[9] == checksum[0] && digits[10] == checksum[1]
}

// INNPersonChecksums returns the two check digits for a 10-digit INN person prefix.
func INNPersonChecksums(tenDigits string) (c1, c2 rune) {
	d := func(i int) int { return int(tenDigits[i] - '0') }
	v1 := ((7*d(0) + 2*d(1) + 4*d(2) + 10*d(3) + 3*d(4) + 5*d(5) + 9*d(6) + 4*d(7) + 6*d(8) + 8*d(9)) % 11) % 10
	digits := [11]int{}
	for i := range 10 {
		digits[i] = int(tenDigits[i] - '0')
	}
	digits[10] = v1
	v2 := weightedChecksum(digits[:], []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8})
	return rune('0' + v1), rune('0' + v2)
}

func weightedChecksum(digits []int, weights []int) int {
	sum := 0
	for i, weight := range weights {
		sum += digits[i] * weight
	}
	return (sum % 11) % 10
}

// INNPersonValid validates a 12-digit INN for individuals.
func INNPersonValid(digits string) bool {
	if len(digits) != 12 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	c1, c2 := INNPersonChecksums(digits[:10])
	return rune(digits[10]) == c1 && rune(digits[11]) == c2
}

// INNOrgChecksum returns the check digit for a 9-digit INN organization prefix.
func INNOrgChecksum(nineDigits string) rune {
	d := func(i int) int { return int(nineDigits[i] - '0') }
	checksum := ((2*d(0) + 4*d(1) + 10*d(2) + 3*d(3) + 5*d(4) + 9*d(5) + 4*d(6) + 6*d(7) + 8*d(8)) % 11) % 10
	return rune('0' + checksum)
}

// INNOrgValid validates a 10-digit INN for organizations.
func INNOrgValid(digits string) bool {
	if len(digits) != 10 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return rune(digits[9]) == INNOrgChecksum(digits[:9])
}

// OGRNCheckDigit returns the check digit for a 12-digit OGRN prefix.
func OGRNCheckDigit(twelveDigits string) rune {
	var n int64
	for _, c := range twelveDigits {
		n = n*10 + int64(c-'0')
	}
	return rune('0' + (n%11)%10)
}

// OGRNValid validates a 13-digit OGRN.
func OGRNValid(digits string) bool {
	if len(digits) != 13 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return rune(digits[12]) == OGRNCheckDigit(digits[:12])
}

// OGRNIPCheckDigit returns the check digit for a 14-digit OGRNIP prefix.
func OGRNIPCheckDigit(fourteenDigits string) rune {
	remainder := 0
	for _, c := range fourteenDigits {
		remainder = (remainder*10 + int(c-'0')) % 13
	}
	return rune('0' + remainder%10)
}

// OGRNIPValid validates a 15-digit OGRNIP.
func OGRNIPValid(digits string) bool {
	if len(digits) != 15 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return rune(digits[14]) == OGRNIPCheckDigit(digits[:14])
}

// IBANMod97 computes the 2-digit MOD-97 check code for an IBAN.
func IBANMod97(countryCode, bban string) string {
	rearranged := strings.ToUpper(bban + countryCode + "00")

	var numStr strings.Builder
	for _, c := range rearranged {
		if c >= 'A' && c <= 'Z' {
			numStr.WriteString(strconv.Itoa(int(c-'A') + 10))
			continue
		}
		numStr.WriteRune(c)
	}

	check := 98 - mod97DecimalString(numStr.String())
	return string([]byte{byte('0' + check/10), byte('0' + check%10)})
}

// IBANMod97Valid validates an IBAN using the MOD-97 algorithm.
func IBANMod97Valid(iban string) bool {
	iban = strings.ToUpper(strings.ReplaceAll(iban, " ", ""))
	if len(iban) < 4 {
		return false
	}
	rearranged := iban[4:] + iban[:4]

	var numStr strings.Builder
	for _, c := range rearranged {
		if unicode.IsLetter(c) {
			numStr.WriteString(strconv.Itoa(int(c-'A') + 10))
			continue
		}
		numStr.WriteRune(c)
	}

	return mod97DecimalString(numStr.String()) == 1
}

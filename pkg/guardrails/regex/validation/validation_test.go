package validation

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		candidate string
		rule      rule.Rule
		want      bool
	}{
		{
			name:      "no validators accepts candidate",
			candidate: "anything",
			rule:      rule.Rule{},
			want:      true,
		},
		{
			name:      "luhn strips formatting before validation",
			candidate: "7992-7398-713",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorLuhn},
			},
			want: true,
		},
		{
			name:      "luhn rejects invalid checksum",
			candidate: "79927398714",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorLuhn},
			},
			want: false,
		},
		{
			name:      "snils accepts valid formatted value",
			candidate: "112-233-445 95",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorSNILS},
			},
			want: true,
		},
		{
			name:      "inn person accepts valid value",
			candidate: "500100732259",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorINNPerson},
			},
			want: true,
		},
		{
			name:      "inn org accepts valid value",
			candidate: "7707083893",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorINNOrg},
			},
			want: true,
		},
		{
			name:      "ogrn accepts valid value",
			candidate: "1027700132195",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorOGRN},
			},
			want: true,
		},
		{
			name:      "ogrnip accepts valid value",
			candidate: "304500116000157",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorOGRNIP},
			},
			want: true,
		},
		{
			name:      "iban accepts valid spaced value",
			candidate: "GB82 WEST 1234 5698 7654 32",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIBANMod97},
			},
			want: true,
		},
		{
			name:      "email accepts valid ascii address",
			candidate: "person.name+tag@example.com",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorEmailASCII},
			},
			want: true,
		},
		{
			name:      "payment card accepts valid visa",
			candidate: "4111 1111 1111 1111",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorPaymentCard},
			},
			want: true,
		},
		{
			name:      "entropy rejects low entropy candidate",
			candidate: "aaaaaaaaaaaaaaaa",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorEntropy},
				Entropy:    3,
			},
			want: false,
		},
		{
			name:      "entropy threshold is ignored when unset",
			candidate: "aaaaaaaaaaaaaaaa",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorEntropy},
			},
			want: true,
		},
		{
			name:      "banlist rejects case insensitive exact match",
			candidate: "Password",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorBanlist},
				Banlist:    []string{"password"},
			},
			want: false,
		},
		{
			name:      "ipv4 accepts ipv4 address",
			candidate: "8.8.8.8",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPv4},
			},
			want: true,
		},
		{
			name:      "ipv6 accepts bracketed ipv6 address",
			candidate: "[2001:4860:4860::8888]",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPv6},
			},
			want: true,
		},
		{
			name:      "public ip rejects private address",
			candidate: "10.1.2.3",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPPublic},
			},
			want: false,
		},
		{
			name:      "private ip accepts cidr private address",
			candidate: "10.1.2.0/24",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPPrivate},
			},
			want: true,
		},
		{
			name:      "multiple validators short circuit on failure",
			candidate: "person@example",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorEmailASCII, rule.ValidatorBanlist},
				Banlist:    []string{"person@example"},
			},
			want: false,
		},
		{
			name:      "unknown validator rejects candidate",
			candidate: "anything",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{"unknown"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Validate(tt.candidate, tt.rule))
		})
	}
}

func TestValidate_RejectsInvalidCandidatesForEachValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		candidate string
		rule      rule.Rule
	}{
		{
			name:      "snils",
			candidate: "11223344596",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorSNILS},
			},
		},
		{
			name:      "inn person",
			candidate: "500100732250",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorINNPerson},
			},
		},
		{
			name:      "inn org",
			candidate: "7707083894",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorINNOrg},
			},
		},
		{
			name:      "ogrn",
			candidate: "1027700132194",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorOGRN},
			},
		},
		{
			name:      "ogrnip",
			candidate: "304500116000158",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorOGRNIP},
			},
		},
		{
			name:      "iban",
			candidate: "GB83 WEST 1234 5698 7654 32",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIBANMod97},
			},
		},
		{
			name:      "email",
			candidate: "person@example",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorEmailASCII},
			},
		},
		{
			name:      "payment card",
			candidate: "4111111111111112",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorPaymentCard},
			},
		},
		{
			name:      "ipv4",
			candidate: "2001:4860:4860::8888",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPv4},
			},
		},
		{
			name:      "ipv6",
			candidate: "8.8.8.8",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPv6},
			},
		},
		{
			name:      "public ip parse failure",
			candidate: "not-ip",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPPublic},
			},
		},
		{
			name:      "private ip parse failure",
			candidate: "not-ip",
			rule: rule.Rule{
				Validators: []rule.ValidatorType{rule.ValidatorIPPrivate},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.False(t, Validate(tt.candidate, tt.rule))
		})
	}
}

func TestIsKnown(t *testing.T) {
	t.Parallel()

	known := []rule.ValidatorType{
		rule.ValidatorLuhn,
		rule.ValidatorSNILS,
		rule.ValidatorINNPerson,
		rule.ValidatorINNOrg,
		rule.ValidatorOGRN,
		rule.ValidatorOGRNIP,
		rule.ValidatorIBANMod97,
		rule.ValidatorEmailASCII,
		rule.ValidatorPaymentCard,
		rule.ValidatorEntropy,
		rule.ValidatorBanlist,
		rule.ValidatorIPv4,
		rule.ValidatorIPv6,
		rule.ValidatorIPPublic,
		rule.ValidatorIPPrivate,
	}
	for _, validator := range known {
		validator := validator
		t.Run(string(validator), func(t *testing.T) {
			t.Parallel()

			assert.True(t, IsKnown(validator))
		})
	}

	assert.False(t, IsKnown("unknown"))
}

func TestChecksumValidators(t *testing.T) {
	t.Parallel()

	t.Run("luhn", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, 70, LuhnSum("79927398713"))
		assert.True(t, LuhnValid("79927398713"))
		assert.False(t, LuhnValid(""))
		assert.False(t, LuhnValid("7992739871x"))
		assert.False(t, LuhnValid("79927398714"))
		assert.Equal(t, '3', LuhnCheckDigit("7992739871"))
	})

	t.Run("snils", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "95", SNILSChecksum("112233445"))
		assert.Equal(t, "00", SNILSChecksum("000029999"))
		assert.Equal(t, "00", SNILSChecksum("123"))
		assert.True(t, SNILSValid("11223344595"))
		assert.False(t, SNILSValid("11223344596"))
		assert.False(t, SNILSValid("1122334459x"))
		assert.False(t, SNILSValid("112233445"))
	})

	t.Run("inn person", func(t *testing.T) {
		t.Parallel()

		c1, c2 := INNPersonChecksums("5001007322")
		assert.Equal(t, '5', c1)
		assert.Equal(t, '9', c2)
		assert.True(t, INNPersonValid("500100732259"))
		assert.False(t, INNPersonValid("500100732250"))
		assert.False(t, INNPersonValid("50010073225x"))
		assert.False(t, INNPersonValid("50010073225"))
	})

	t.Run("inn org", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, '3', INNOrgChecksum("770708389"))
		assert.True(t, INNOrgValid("7707083893"))
		assert.False(t, INNOrgValid("7707083894"))
		assert.False(t, INNOrgValid("770708389x"))
		assert.False(t, INNOrgValid("770708389"))
	})

	t.Run("ogrn", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, '5', OGRNCheckDigit("102770013219"))
		assert.True(t, OGRNValid("1027700132195"))
		assert.False(t, OGRNValid("1027700132194"))
		assert.False(t, OGRNValid("102770013219x"))
		assert.False(t, OGRNValid("102770013219"))
	})

	t.Run("ogrnip", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, '7', OGRNIPCheckDigit("30450011600015"))
		assert.True(t, OGRNIPValid("304500116000157"))
		assert.False(t, OGRNIPValid("304500116000158"))
		assert.False(t, OGRNIPValid("30450011600015x"))
		assert.False(t, OGRNIPValid("30450011600015"))
	})

	t.Run("iban", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "82", IBANMod97("GB", "WEST12345698765432"))
		assert.True(t, IBANMod97Valid("GB82 WEST 1234 5698 7654 32"))
		assert.False(t, IBANMod97Valid("GB83 WEST 1234 5698 7654 32"))
		assert.False(t, IBANMod97Valid("GB1"))
	})
}

func TestEmailASCIIValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{name: "valid with supported local chars", email: "a.!#$%&'*+-/=?^_`{|}~@example.com", want: true},
		{name: "trim spaces", email: " person@example.com ", want: true},
		{name: "empty", email: "", want: false},
		{name: "missing at", email: "person.example.com", want: false},
		{name: "empty local", email: "@example.com", want: false},
		{name: "empty domain", email: "person@", want: false},
		{name: "double dot in local", email: "person..name@example.com", want: false},
		{name: "leading dot in local", email: ".person@example.com", want: false},
		{name: "trailing dot in local", email: "person.@example.com", want: false},
		{name: "invalid local char", email: "person,name@example.com", want: false},
		{name: "single label domain", email: "person@example", want: false},
		{name: "empty domain label", email: "person@example..com", want: false},
		{name: "leading domain hyphen", email: "person@-example.com", want: false},
		{name: "trailing domain hyphen", email: "person@example-.com", want: false},
		{name: "numeric tld", email: "person@example.123", want: false},
		{name: "invalid domain char", email: "person@exa_mple.com", want: false},
		{name: "local too long", email: strings.Repeat("a", 65) + "@example.com", want: false},
		{name: "domain too long", email: "person@" + strings.Repeat("a", 250) + ".com", want: false},
		{name: "label too long", email: "person@" + strings.Repeat("a", 64) + ".com", want: false},
		{name: "address too long", email: strings.Repeat("a", 64) + "@" + strings.Repeat("b", 185) + ".com", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, EmailASCIIValid(tt.email))
		})
	}
}

func TestPaymentCardValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		digits string
		want   bool
	}{
		{name: "visa 13", digits: validPAN("4", 13), want: true},
		{name: "visa 16", digits: "4111111111111111", want: true},
		{name: "visa 19", digits: validPAN("4", 19), want: true},
		{name: "amex 34", digits: validPAN("34", 15), want: true},
		{name: "amex 37", digits: validPAN("37", 15), want: true},
		{name: "mastercard 51", digits: validPAN("51", 16), want: true},
		{name: "mastercard 2221", digits: validPAN("2221", 16), want: true},
		{name: "mastercard 2720", digits: validPAN("2720", 16), want: true},
		{name: "discover 6011", digits: validPAN("6011", 16), want: true},
		{name: "discover 644", digits: validPAN("644", 16), want: true},
		{name: "discover 65", digits: validPAN("65", 19), want: true},
		{name: "unionpay 62", digits: validPAN("62", 16), want: true},
		{name: "mir 2200", digits: validPAN("2200", 16), want: true},
		{name: "mir 2204", digits: validPAN("2204", 19), want: true},
		{name: "empty", digits: "", want: false},
		{name: "non digit", digits: "411111111111111x", want: false},
		{name: "too short", digits: validPAN("4", 12), want: false},
		{name: "too long", digits: validPAN("4", 20), want: false},
		{name: "bad luhn", digits: "4111111111111112", want: false},
		{name: "unknown prefix", digits: validPAN("99", 16), want: false},
		{name: "visa invalid length", digits: validPAN("4", 15), want: false},
		{name: "amex invalid length", digits: validPAN("34", 16), want: false},
		{name: "mastercard low range", digits: validPAN("50", 16), want: false},
		{name: "mastercard high range", digits: validPAN("56", 16), want: false},
		{name: "mastercard 2220 below range", digits: validPAN("2220", 16), want: false},
		{name: "mastercard 2721 above range", digits: validPAN("2721", 16), want: false},
		{name: "discover 643 below range", digits: validPAN("643", 16), want: false},
		{name: "mir 2205 above range", digits: validPAN("2205", 16), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, PaymentCardValid(tt.digits))
		})
	}
}

func TestIPValidationHelpersViaValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		candidate string
		validator rule.ValidatorType
		want      bool
	}{
		{name: "invalid ip", candidate: "not-ip", validator: rule.ValidatorIPv4, want: false},
		{name: "ipv4 cidr", candidate: "8.8.8.0/24", validator: rule.ValidatorIPv4, want: true},
		{name: "ipv4 is not ipv6", candidate: "8.8.8.8", validator: rule.ValidatorIPv6, want: false},
		{name: "ipv6 public", candidate: "2001:4860:4860::8888", validator: rule.ValidatorIPPublic, want: true},
		{name: "loopback is not public", candidate: "127.0.0.1", validator: rule.ValidatorIPPublic, want: false},
		{name: "multicast is private-or-local", candidate: "224.0.0.1", validator: rule.ValidatorIPPrivate, want: true},
		{name: "unspecified is private-or-local", candidate: "0.0.0.0", validator: rule.ValidatorIPPrivate, want: true},
		{name: "public is not private", candidate: "8.8.8.8", validator: rule.ValidatorIPPrivate, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Validate(tt.candidate, rule.Rule{
				Validators: []rule.ValidatorType{tt.validator},
			}))
		})
	}
}

func TestShannonEntropy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want float64
	}{
		{name: "empty", in: "", want: 0},
		{name: "single repeated ascii", in: "aaaa", want: 0},
		{name: "balanced ascii", in: "abcd", want: 2},
		{name: "non ascii runes", in: "абвг", want: 2},
		{name: "non ascii repeated runes", in: "яяяя", want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tt.want, ShannonEntropy(tt.in), 0.000001)
		})
	}
}

func TestInternalHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "123", stripNonDigits("a1-2 3b"))
	assert.True(t, banlisted("Password", []string{"password"}))
	assert.False(t, banlisted("password1", []string{"password"}))

	assert.True(t, allDigits("123"))
	assert.False(t, allDigits(""))
	assert.False(t, allDigits("12x"))

	assert.True(t, prefixInRange("2221", 4, 2221, 2720))
	assert.False(t, prefixInRange("22", 4, 2221, 2720))
	assert.False(t, prefixInRange("22x1", 4, 2221, 2720))
	assert.False(t, prefixInRange("2721", 4, 2221, 2720))

	assert.True(t, isASCIILocalPartChar('~'))
	assert.True(t, isASCIILocalPartChar('a'))
	assert.True(t, isASCIILocalPartChar('A'))
	assert.True(t, isASCIILocalPartChar('1'))
	assert.False(t, isASCIILocalPartChar(','))
	assert.True(t, isASCIIDomainChar('-'))
	assert.False(t, isASCIIDomainChar('_'))
	assert.True(t, isAlpha("COM"))
	assert.False(t, isAlpha("c0m"))
	assert.False(t, isAlpha(""))

	assert.Equal(t, 1, mod97DecimalString("98"))

	_, ok := parseIPCandidate("   ")
	assert.False(t, ok)
}

func validPAN(prefix string, length int) string {
	if length <= len(prefix) {
		return prefix
	}
	body := prefix + strings.Repeat("0", length-len(prefix)-1)
	return body + string(LuhnCheckDigit(body))
}

func TestEntropySanityForNonPowerDistribution(t *testing.T) {
	t.Parallel()

	got := ShannonEntropy("aaab")
	want := -(0.75 * math.Log2(0.75)) - (0.25 * math.Log2(0.25))
	assert.InDelta(t, want, got, 0.000001)
}

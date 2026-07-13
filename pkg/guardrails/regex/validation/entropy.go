package validation

import "math"

// ShannonEntropy calculates the Shannon entropy of a string in bits per character.
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}

	ascii := true
	for i := range len(s) {
		if s[i] > 127 {
			ascii = false
			break
		}
	}

	if ascii {
		var freq [256]int
		for i := range len(s) {
			freq[s[i]]++
		}
		n := float64(len(s))
		var entropy float64
		for _, count := range freq {
			if count == 0 {
				continue
			}
			p := float64(count) / n
			entropy -= p * math.Log2(p)
		}
		return entropy
	}

	freq := make(map[rune]int)
	for _, c := range s {
		freq[c]++
	}
	n := float64(len([]rune(s)))
	var entropy float64
	for _, count := range freq {
		p := float64(count) / n
		entropy -= p * math.Log2(p)
	}
	return entropy
}

package demask

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

// Demasker demasks LLM response text using shared DemaskConfig and a per-stream pending suffix.
type Demasker struct {
	cfg     *DemaskConfig
	pending string
	jsonCtx bool // restored originals are JSON-escaped (see Factory.JSONDemasker)
}

// NewDemasker builds a Demasker backed by the provided request-scoped config.
func NewDemasker(cfg *DemaskConfig) *Demasker {
	return &Demasker{cfg: cfg}
}

// DemaskChunk appends chunk to internal buffer, applies exact and placeholder regex demasking.
// If flush is false, returns the safe prefix and retains a tail of at least maxPending bytes
// so a placeholder that straddles the chunk boundary is preserved.
func (d *Demasker) DemaskChunk(ctx context.Context, chunk string, flush bool) (string, error) {
	text := d.pending + chunk
	d.pending = ""

	if text == "" {
		return "", nil
	}

	var err error
	text = d.applyExact(text)
	text, err = d.applyPlaceholderRegex(text)
	if err != nil {
		// Demasking failed. Hand back everything not yet emitted — the
		// exact-demasked buffer with any unresolved placeholders left intact —
		// so the caller can emit it as a fail-open, lossless fallback instead
		// of dropping the stream tail. pending was already cleared above.
		return text, err
	}

	if flush {
		return text, nil
	}

	safeEnd := len(text) - d.cfg.maxPending
	if safeEnd <= 0 {
		d.pending = text
		return "", nil
	}

	safeEnd = utf8SafePrefixEnd(text, safeEnd)
	if safeEnd <= 0 {
		d.pending = text
		return "", nil
	}

	d.pending = text[safeEnd:]
	return text[:safeEnd], nil
}

func utf8SafePrefixEnd(s string, end int) int {
	if end <= 0 {
		return 0
	}
	if end >= len(s) {
		return len(s)
	}

	i := end - 1
	for i >= 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	if i < 0 {
		return 0
	}

	_, size := utf8.DecodeRuneInString(s[i:])
	if i+size <= end {
		return end
	}
	return i
}

func (d *Demasker) applyExact(text string) string {
	replacer := d.cfg.exactReplacer
	if d.jsonCtx {
		replacer = d.cfg.jsonReplacer
	}
	if replacer == nil {
		return text
	}
	return replacer.Replace(text)
}

type textReplacement struct {
	start, end int
	original   string
}

func (d *Demasker) applyPlaceholderRegex(text string) (string, error) {
	dc := d.cfg
	if dc.scan == nil {
		return text, nil
	}

	matches, err := dc.scan(text)
	if err != nil {
		return text, err
	}
	if len(matches) == 0 {
		return text, nil
	}

	originals := dc.placeholderToOriginal
	if d.jsonCtx {
		originals = dc.placeholderToJSON
	}

	replacements := make([]textReplacement, 0, len(matches))
	for _, match := range matches {
		if match.Placeholder == "" {
			continue
		}
		orig, ok := originals[match.Placeholder]
		if !ok {
			continue
		}
		replacements = append(replacements, textReplacement{
			start:    match.Start,
			end:      match.End,
			original: orig,
		})
	}

	return applyTextReplacements(text, replacements), nil
}

func applyTextReplacements(text string, replacements []textReplacement) string {
	if len(replacements) == 0 {
		return text
	}

	sort.Slice(replacements, func(i, j int) bool {
		if replacements[i].start != replacements[j].start {
			return replacements[i].start < replacements[j].start
		}
		return replacements[i].end > replacements[j].end
	})

	var b strings.Builder
	b.Grow(len(text))
	pos := 0
	for _, rep := range replacements {
		if rep.start < pos {
			continue
		}
		b.WriteString(text[pos:rep.start])
		b.WriteString(rep.original)
		pos = rep.end
	}
	b.WriteString(text[pos:])
	return b.String()
}

package placeholderfmt

import "fmt"

// Format returns the canonical synthetic placeholder for a placeholder type and index.
func Format(placeholderType string, index int) string {
	return fmt.Sprintf("<%s_%d>", placeholderType, index)
}

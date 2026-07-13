package repository

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EncodeAuditCursor builds the opaque keyset cursor pointing at the last
// record of a page: listing resumes strictly after (ts, requestID) in
// (Timestamp desc, RequestID desc) order.
func EncodeAuditCursor(ts time.Time, requestID string) string {
	raw := strconv.FormatInt(ts.UnixNano(), 10) + ":" + requestID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeAuditCursor parses a cursor produced by EncodeAuditCursor.
// Returns ErrBadCursor when the input is not a valid cursor.
func DecodeAuditCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %s", ErrBadCursor, err)
	}
	nanosStr, requestID, ok := strings.Cut(string(raw), ":")
	if !ok || requestID == "" {
		return time.Time{}, "", ErrBadCursor
	}
	nanos, err := strconv.ParseInt(nanosStr, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %s", ErrBadCursor, err)
	}
	return time.Unix(0, nanos).UTC(), requestID, nil
}

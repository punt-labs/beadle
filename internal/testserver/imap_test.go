package testserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
)

// TestMatchesCriteria_DateGranularity proves SENTSINCE and SINCE match at DATE
// precision, as real IMAP does: a message sent earlier in the same day as a
// non-midnight Since still matches, and the day before does not. This is the
// contract the fake must uphold so green tests reflect production behavior.
func TestMatchesCriteria_DateGranularity(t *testing.T) {
	day := func(y int, m time.Month, d, h int) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
	}
	rawWithDate := func(sent time.Time) []byte {
		return []byte(fmt.Sprintf("From: a@test.com\r\nSubject: x\r\nDate: %s\r\n\r\nbody",
			sent.Format(time.RFC1123Z)))
	}

	// A non-midnight cutoff late in the day; the same-day morning message must
	// still match because the time of day is ignored.
	cutoff := day(2026, 7, 10, 18)
	sameDay := day(2026, 7, 10, 6)
	dayBefore := day(2026, 7, 9, 23)

	t.Run("SENTSINCE same-day earlier time matches", func(t *testing.T) {
		msg := &memMessage{raw: rawWithDate(sameDay), date: sameDay}
		assert.True(t, matchesCriteria(msg, &imap.SearchCriteria{SentSince: cutoff}))
	})
	t.Run("SENTSINCE day before excluded", func(t *testing.T) {
		msg := &memMessage{raw: rawWithDate(dayBefore), date: dayBefore}
		assert.False(t, matchesCriteria(msg, &imap.SearchCriteria{SentSince: cutoff}))
	})
	t.Run("SINCE same-day earlier time matches (INTERNALDATE)", func(t *testing.T) {
		msg := &memMessage{raw: rawWithDate(sameDay), date: sameDay}
		assert.True(t, matchesCriteria(msg, &imap.SearchCriteria{Since: cutoff}))
	})
	t.Run("SINCE day before excluded", func(t *testing.T) {
		msg := &memMessage{raw: rawWithDate(dayBefore), date: dayBefore}
		assert.False(t, matchesCriteria(msg, &imap.SearchCriteria{Since: cutoff}))
	})
}

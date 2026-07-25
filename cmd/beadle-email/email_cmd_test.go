package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/email"
)

// TestChangeStatus proves a mutating command reports "not_found" when nothing
// changed, so mark/move JSON never reads as success for a stale UID.
func TestChangeStatus(t *testing.T) {
	assert.Equal(t, "not_found", changeStatus(0, "marked"))
	assert.Equal(t, "marked", changeStatus(1, "marked"))
	assert.Equal(t, "not_found", changeStatus(0, "moved"))
	assert.Equal(t, "moved", changeStatus(3, "moved"))
}

// TestListCmd_RejectsBadPaging proves list validates count and offset before
// opening a connection, so a bad value is a deterministic error, not a silent
// empty result. The checks run first in RunE, so no server is contacted.
func TestListCmd_RejectsBadPaging(t *testing.T) {
	orig := struct {
		count, offset int
	}{listCount, listOffset}
	t.Cleanup(func() { listCount, listOffset = orig.count, orig.offset })

	listOffset = 0
	listCount = 0
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--count must be positive")

	listCount = -5
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--count must be positive")

	listCount = 10
	listOffset = -1
	require.ErrorContains(t, listCmd.RunE(listCmd, nil), "--offset must be non-negative")
}

// TestSearchCmd_RejectsBadPaging proves search validates count and offset up
// front, after the criteria check and before connecting.
func TestSearchCmd_RejectsBadPaging(t *testing.T) {
	orig := struct {
		from          string
		count, offset int
	}{searchFrom, searchCount, searchOffset}
	t.Cleanup(func() {
		searchFrom, searchCount, searchOffset = orig.from, orig.count, orig.offset
	})

	searchFrom = "alice@test.com" // satisfy the "at least one criterion" gate
	searchOffset = 0
	searchCount = 0
	require.ErrorContains(t, searchCmd.RunE(searchCmd, nil), "--count must be positive")

	searchCount = 10
	searchOffset = -2
	require.ErrorContains(t, searchCmd.RunE(searchCmd, nil), "--offset must be non-negative")
}

func sampleMessages() []channel.MessageSummary {
	when := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	return []channel.MessageSummary{
		{ID: "7", From: "alice@test.com", Date: when, Subject: "hello"},
	}
}

// TestPrintMessages_PastEndPageShowsTotal proves the CLI surfaces the true
// total on a page past the end, rather than a bare empty result that reads as
// an empty mailbox.
func TestPrintMessages_PastEndPageShowsTotal(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	g.printMessages(&buf, &email.ListResult{Total: 25})

	out := buf.String()
	assert.Contains(t, out, "showing 0 of 25 messages")
	assert.Contains(t, out, "page past end")
	assert.NotEmpty(t, out, "a past-end page must not print nothing")
}

// TestPrintMessages_DegradedNoticeUnderQuiet proves the degraded notice reaches
// the user even with --quiet, when the stderr warn is hidden and the rows are
// recent mail, not the requested query.
func TestPrintMessages_DegradedNoticeUnderQuiet(t *testing.T) {
	g := globalOpts{Quiet: true}
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, "search unavailable; showing recent mail instead",
		"the degraded notice must appear even under --quiet")
	assert.NotContains(t, out, "showing 1 of 1 messages",
		"--quiet still suppresses the normal status line and rows")
}

// TestPrintMessages_NormalListing shows the status line and rows without a
// degraded notice on an ordinary listing.
func TestPrintMessages_NormalListing(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	g.printMessages(&buf, &email.ListResult{Messages: sampleMessages(), Total: 1})

	out := buf.String()
	assert.Contains(t, out, "showing 1 of 1 messages")
	assert.Contains(t, out, "hello")
	assert.NotContains(t, out, "degraded")
	assert.NotContains(t, out, "recent mail instead")
}

// TestPrintMessages_DegradedShownWithRows proves that, when not quiet, the
// degraded notice leads and the status line and rows follow.
func TestPrintMessages_DegradedShownWithRows(t *testing.T) {
	var g globalOpts
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, "search unavailable; showing recent mail instead")
	assert.Contains(t, out, "showing 1 of 1 messages")
	assert.Contains(t, out, "hello")
}

// TestPrintMessages_JSONUnchanged proves --json still emits the message slice.
func TestPrintMessages_JSONUnchanged(t *testing.T) {
	g := globalOpts{JSON: true}
	var buf bytes.Buffer
	lr := &email.ListResult{
		Messages:       sampleMessages(),
		Total:          1,
		Degraded:       true,
		DegradedReason: "search unavailable; showing recent mail instead",
	}
	g.printMessages(&buf, lr)

	out := buf.String()
	assert.Contains(t, out, `"id": "7"`, "JSON carries the message slice")
	assert.NotContains(t, out, "showing 1 of 1 messages", "no human status line in JSON mode")
	assert.NotContains(t, out, "recent mail instead", "no human notice in JSON mode")
}

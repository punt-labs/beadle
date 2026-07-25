package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/email"
)

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

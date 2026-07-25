package email

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreading_WriteHeaders(t *testing.T) {
	tests := []struct {
		name      string
		threading Threading
		want      string
		wantErr   string
	}{
		{"zero writes nothing", Threading{}, "", ""},
		{
			"in-reply-to only",
			Threading{InReplyTo: "<a@host>"},
			"In-Reply-To: <a@host>\r\n",
			"",
		},
		{
			"both headers",
			Threading{InReplyTo: "<b@host>", References: []string{"<a@host>", "<b@host>"}},
			"In-Reply-To: <b@host>\r\nReferences: <a@host> <b@host>\r\n",
			"",
		},
		{
			"references only",
			Threading{References: []string{"<a@host>"}},
			"References: <a@host>\r\n",
			"",
		},
		{
			"CR in in-reply-to rejected",
			Threading{InReplyTo: "<a@host>\r\nBcc: evil@x"},
			"",
			"CR/LF",
		},
		{
			"LF in references rejected",
			Threading{References: []string{"<a@host>\nBcc: evil@x"}},
			"",
			"CR/LF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := tt.threading.writeHeaders(&buf)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestThreading_Headers(t *testing.T) {
	assert.Nil(t, Threading{}.headers(), "zero Threading yields no Resend headers")

	h := Threading{InReplyTo: "<b@host>", References: []string{"<a@host>", "<b@host>"}}.headers()
	assert.Equal(t, map[string]string{
		"In-Reply-To": "<b@host>",
		"References":  "<a@host> <b@host>",
	}, h)
}

func TestBuildReferences(t *testing.T) {
	tests := []struct {
		name       string
		references string
		inReplyTo  string
		messageID  string
		want       []string
	}{
		{
			"existing references chain, append message-id",
			"<a@host> <b@host>", "<b@host>", "<c@host>",
			[]string{"<a@host>", "<b@host>", "<c@host>"},
		},
		{
			"no references falls back to in-reply-to",
			"", "<a@host>", "<b@host>",
			[]string{"<a@host>", "<b@host>"},
		},
		{
			"no references and no in-reply-to: bare message-id",
			"", "", "<b@host>",
			[]string{"<b@host>"},
		},
		{
			"no message-id: chain unchanged",
			"<a@host>", "", "",
			[]string{"<a@host>"},
		},
		{
			"nothing at all",
			"", "", "",
			nil,
		},
		{
			"whitespace-padded message-id is trimmed",
			"", "", "  <b@host>  ",
			[]string{"<b@host>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildReferences(tt.references, tt.inReplyTo, tt.messageID))
		})
	}
}

func TestReplySubject(t *testing.T) {
	tag := RepoTag{Slug: "punt-labs/beadle", Agent: "claude"}
	tests := []struct {
		name     string
		original string
		tag      RepoTag
		want     string
	}{
		{"plain, no tag", "Hello", RepoTag{}, "Re: Hello"},
		{"plain, tagged", "Hello", tag, "Re: [punt-labs/beadle] Hello"},
		{"already Re, tagged", "Re: [punt-labs/beadle] Hello", tag, "Re: [punt-labs/beadle] Hello"},
		{"already Re untagged, add tag", "Re: Hello", tag, "Re: [punt-labs/beadle] Hello"},
		{"tagged without Re, add Re", "[punt-labs/beadle] Hello", tag, "Re: [punt-labs/beadle] Hello"},
		{"forward gains Re", "Fwd: Hello", tag, "Re: Fwd: [punt-labs/beadle] Hello"},
		{"case-insensitive RE", "RE: Hello", tag, "RE: [punt-labs/beadle] Hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplySubject(tt.original, tt.tag)
			assert.Equal(t, tt.want, got)
			// Idempotent: replying to a reply subject changes nothing.
			assert.Equal(t, got, ReplySubject(got, tt.tag), "ReplySubject must be idempotent")
		})
	}
}

func TestQuoteBody(t *testing.T) {
	date := time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC)

	t.Run("new text, attribution, quoted body", func(t *testing.T) {
		got := QuoteBody("Thanks, will do.", "Alice <alice@x.com>", date, "line one\nline two")
		want := "Thanks, will do.\n\n" +
			"On Sat, 25 Jul 2026 09:30 UTC, Alice <alice@x.com> wrote:\n" +
			"> line one\n" +
			"> line two\n"
		assert.Equal(t, want, got)
	})

	t.Run("blank internal line becomes bare marker", func(t *testing.T) {
		got := QuoteBody("ok", "a@x.com", date, "one\n\ntwo")
		assert.Contains(t, got, "> one\n>\n> two\n")
	})

	t.Run("CRLF body normalized and trailing newline trimmed", func(t *testing.T) {
		got := QuoteBody("ok", "a@x.com", date, "one\r\ntwo\r\n")
		assert.Contains(t, got, "> one\n> two\n")
		assert.NotContains(t, got, ">\n> two\n>\n")
	})

	t.Run("empty original body: only new text and attribution", func(t *testing.T) {
		got := QuoteBody("just this", "a@x.com", date, "")
		assert.Equal(t, "just this\n\nOn Sat, 25 Jul 2026 09:30 UTC, a@x.com wrote:\n", got)
	})

	t.Run("attribution degrades without sender or date", func(t *testing.T) {
		got := QuoteBody("hi", "", time.Time{}, "body")
		assert.Contains(t, got, "Quoting the original message:\n")
	})
}

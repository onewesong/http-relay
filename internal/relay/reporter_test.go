package relay

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLogReporterIncludesRouteMetadata(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	reporter := newLogReporter(log.New(&out, "", 0), Palette{})
	reporter.Access(AccessRecord{
		Namespace:      "team-a",
		RewriteProfile: "openai",
		Method:         "GET",
		Target:         "https://example.com/",
		Status:         200,
	})

	got := out.String()
	for _, want := range []string{`namespace="team-a"`, `rewrite_profile="openai"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("access log %q does not contain %q", got, want)
		}
	}
}

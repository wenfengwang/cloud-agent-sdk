package gaps

import (
	"os"
	"strings"
	"testing"
)

// TestMatchesDesign keeps the registry identical to the SDK Gaps table of
// design 001, which owns it.
func TestMatchesDesign(t *testing.T) {
	doc, err := os.ReadFile("../../design/001-architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(doc), "\n## SDK Gaps\n")
	if !ok {
		t.Fatal("design has no SDK Gaps section")
	}
	table, _, _ = strings.Cut(table, "\n## ")

	var want []Gap
	for _, line := range strings.Split(table, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 5 || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			continue
		}
		want = append(want, Gap{
			ID:     strings.Trim(strings.TrimSpace(cells[1]), "`"),
			Reason: strings.ReplaceAll(strings.TrimSpace(cells[3]), "`", ""), // shown as plain text
		})
	}
	got := All()
	if len(got) != len(want) {
		t.Fatalf("registry has %d gaps, design has %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("gap %d = %+v, design has %+v", i, got[i], want[i])
		}
	}
}

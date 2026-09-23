package lumoCmd

import (
	"testing"

	"github.com/major0/proton-utils/api/lumo"
)

func TestWebSearchTools(t *testing.T) {
	if got := webSearchTools(false); got != nil {
		t.Fatalf("webSearchTools(false) = %v, want nil", got)
	}
	got := webSearchTools(true)
	if len(got) != 1 || got[0] != lumo.ToolWebSearch {
		t.Fatalf("webSearchTools(true) = %v, want [%q]", got, lumo.ToolWebSearch)
	}
}

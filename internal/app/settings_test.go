package app

import "testing"

// TestRemoveProviderWithAnEmptyReplacementModelList pins the fallback:
// addProvider creates with an empty model list, so the provider taking
// the active slot may have none to select. Indexing Models[0] panicked —
// there is no recover anywhere in the process.
func TestRemoveProviderWithAnEmptyReplacementModelList(t *testing.T) {
	a := newTestApp(t)
	a.providers = []Provider{
		{ID: "empty", Name: "No Models"},
		{ID: "p2", Name: "P2", Models: []string{"m1"}},
	}
	a.providerID = "p2"
	a.model = "m1"

	a.removeProvider("p2")

	if a.providerID != "empty" {
		t.Fatalf("provider selection not repaired: %q", a.providerID)
	}
	if a.model != "" {
		t.Fatalf("the model should fall back to empty, got %q", a.model)
	}
}

// TestRemoveProviderPicksTheReplacementModel is the control: a
// replacement that does have models still donates its first.
func TestRemoveProviderPicksTheReplacementModel(t *testing.T) {
	a := newTestApp(t)
	a.providers = []Provider{
		{ID: "p1", Name: "P1", Models: []string{"x1", "x2"}},
		{ID: "p2", Name: "P2", Models: []string{"m1"}},
	}
	a.providerID = "p2"
	a.model = "m1"

	a.removeProvider("p2")

	if a.providerID != "p1" || a.model != "x1" {
		t.Fatalf("got %q/%q, want p1/x1", a.providerID, a.model)
	}
}

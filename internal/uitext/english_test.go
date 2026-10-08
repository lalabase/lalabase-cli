package uitext

import "testing"

func TestCatalogueUsesTheSameBoundary(t *testing.T) {
	if Inline(App).String() != "lalabase" || Body(NoData).String() != "No data loaded" {
		t.Fatal("catalogue has no shell text")
	}
	for id := ID(0); id < count; id++ {
		if Inline(id).String() == "" || Body(id).String() == "" {
			t.Fatalf("missing catalogue entry %d", id)
		}
	}
	if Inline(ID(255)).String() != "" || Body(ID(255)).String() != "" {
		t.Fatal("unknown catalogue ID is not empty")
	}
	// A developer mislabelling foreign text still cannot bypass cleaning.
	saved := english[App]
	defer func() { english[App] = saved }()
	english[App] = "a\x1b[31m\n\tb"
	if Inline(App).String() != "a [31m b" || Body(App).String() != "a [31m\n\tb" {
		t.Fatal("catalogue bypassed the cleaning constructors")
	}
}

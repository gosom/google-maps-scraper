//nolint:testpackage // Exercise internal location resolution and grid planning.
package mcpserver

import (
	"testing"
)

func TestPlanModesAndCoverage(t *testing.T) {
	in := startInput{
		Query: "cafés", Center: &point{35.17, 33.36}, RadiusKM: 2,
		Coverage: "broad", Fields: []string{"emails", "review_count"},
	}

	p, _, err := makePlan(&in)
	if err != nil || p.Mode != "fast" || !p.Email || len(p.Cells) < 2 {
		t.Fatalf("fast grid with email enrichment: %+v, %v", p, err)
	}

	in.Fields = append(in.Fields, "reviews")

	p, _, err = makePlan(&in)
	if err != nil || p.Mode != "detailed" || !p.Email || p.ExtraReviews {
		t.Fatalf("review text without extra collection: %+v, %v", p, err)
	}

	in.Mode = "fast"
	if _, _, err := makePlan(&in); err == nil {
		t.Fatal("explicit fast mode accepted review text")
	}
}

func TestResolvePlace(t *testing.T) {
	p, choices, err := makePlan(&startInput{Query: "cafés", Location: "Nicosia, Cyprus"})
	if err != nil || len(choices) != 0 || p.Location != "Nicosia, Cyprus" || p.Center.Lat < 35 || p.Center.Lat > 36 {
		t.Fatalf("Nicosia lookup: %+v, %+v, %v", p, choices, err)
	}

	_, choices, err = makePlan(&startInput{Query: "cafés", Location: "Springfield"})
	if err != nil || len(choices) < 2 {
		t.Fatalf("Springfield lookup: %+v, %v", choices, err)
	}

	p, _, err = makePlan(&startInput{Query: "cafés", LocationID: choices[0].ID})
	if err != nil || p.Center != choices[0].Center {
		t.Fatalf("resolving a location choice: %+v, %v", p, err)
	}
}

func TestNamedCityUsesGridByDefault(t *testing.T) {
	for _, place := range []string{"Latsia", "Lakkia, Cyprus"} {
		in := startInput{Query: "dentists", Location: place}

		p, choices, err := makePlan(&in)
		if err != nil || len(choices) != 0 || p.Location != "Latsia, Cyprus" ||
			!p.Approximate || p.Mode != "fast" || len(p.Cells) < 2 || len(p.Cells) > maxCells {
			t.Fatalf("%s should resolve to a bounded fast grid: %+v, %v, %v", place, p, choices, err)
		}
	}

	for _, in := range []startInput{
		{Query: "dentists", Location: "Latsia", Coverage: "standard"},
		{Query: "dentists", Location: "Latsia", RadiusKM: 2},
	} {
		p, _, err := makePlan(&in)
		if err != nil || p.Approximate || len(p.Cells) != 1 {
			t.Fatalf("nearby search should use one center: %+v, %v", p, err)
		}
	}
}

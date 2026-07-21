package resume_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/runner/resume"
	"github.com/stretchr/testify/require"
)

func TestLoadCSVIdentities(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.csv")
	body := "input_id,link,title,cid,data_id,place_id\n" +
		"q1,https://maps/place/a,A,cid-a,data-a,place-a\n" +
		"q2,https://maps/place/b,B,,data-b,\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	ids, err := resume.LoadResultIdentities(path, false)

	require.NoError(t, err)
	require.True(t, ids.Has("place-a"))
	require.True(t, ids.Has("cid-a"))
	require.True(t, ids.Has("data-b"))
}

func TestLoadJSONLIdentities(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	body := `{"input_id":"q1","link":"https://maps/place/a","place_id":"place-a"}` + "\n" +
		`{"input_id":"q2","link":"https://maps/place/b","cid":"cid-b"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	ids, err := resume.LoadResultIdentities(path, true)

	require.NoError(t, err)
	require.True(t, ids.Has("place-a"))
	require.True(t, ids.Has("cid-b"))
}

func TestLoadJSONLIdentitiesSupportsLargeEntries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	body := `{"input_id":"q1","link":"https://maps/place/a","place_id":"place-a","description":"` +
		strings.Repeat("x", 70*1024) + `"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	ids, err := resume.LoadResultIdentities(path, true)

	require.NoError(t, err)
	require.True(t, ids.Has("place-a"))
}

func TestLoadResultIdentitiesMissingFile(t *testing.T) {
	t.Parallel()

	ids, err := resume.LoadResultIdentities(filepath.Join(t.TempDir(), "missing.csv"), false)

	require.NoError(t, err)
	require.False(t, ids.Has("anything"))
}

func TestIdentitySetCloneIsIndependent(t *testing.T) {
	t.Parallel()

	emitted := resume.NewIdentitySet()
	emitted.Add("https://maps/place/existing")
	discovered := emitted.Clone()

	discovered.Add("https://maps/place/new")

	require.True(t, discovered.Has("https://maps/place/new"))
	require.False(t, emitted.Has("https://maps/place/new"))
}

func TestIdentitySetMatchesAnyEntryIdentity(t *testing.T) {
	t.Parallel()

	ids := resume.NewIdentitySet()
	ids.Add("cid-a")

	require.True(t, ids.HasEntry(&gmaps.Entry{
		PlaceID: "place-a",
		Cid:     "cid-a",
	}))
}

func TestLoadJSONLIdentitiesRepairsIncompleteTrailingRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	body := `{"place_id":"place-a"}` + "\n" + `{"place_id":"place-b"`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	ids, err := resume.LoadResultIdentities(path, true)

	require.NoError(t, err)
	require.True(t, ids.Has("place-a"))
	require.False(t, ids.Has("place-b"))

	repaired, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, `{"place_id":"place-a"}`+"\n", string(repaired))
}

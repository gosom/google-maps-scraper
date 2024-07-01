package utils

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateH3Listing(t *testing.T) {
	t.Run("ValidPolygon", func(t *testing.T) {
		polygon := [][]float64{
			{
				106.7151833,
				10.8385326,
			},
			{
				106.7139387,
				10.8271098,
			},
			{
				106.7195606,
				10.8217144,
			},
			{
				106.7323065,
				10.8275735,
			},
			{
				106.7332077,
				10.8407665,
			},
			{
				106.7149687,
				10.8385326,
			},
		}
		resolution := 12

		h3Listing, err := GenerateH3Listing(polygon, resolution)
		assert.NoError(t, err)
		assert.NotEmpty(t, h3Listing)
		fmt.Println(h3Listing)
	})

	t.Run("EmptyPolygon", func(t *testing.T) {
		polygon := [][]float64{}
		resolution := 12

		h3Listing, err := GenerateH3Listing(polygon, resolution)
		assert.NoError(t, err)
		assert.Empty(t, h3Listing)
	})

	t.Run("InvalidResolution", func(t *testing.T) {
		polygon := [][]float64{
			{37.7749, -122.4194},
			{37.7749, -122.4194},
			{37.7749, -122.4194},
			{37.7749, -122.4194},
		}
		resolution := -1

		h3Listing, err := GenerateH3Listing(polygon, resolution)
		assert.Error(t, err)
		assert.Empty(t, h3Listing)
		fmt.Println(h3Listing)

	})
}

func TestLocationToString(t *testing.T) {
	t.Run("ValidLocation", func(t *testing.T) {
		location := []float64{10.7773285, 106.6864011}
		expected := "10.777328,106.686401"
		result := LocationToString(location)
		assert.Equal(t, expected, result)
	})

	t.Run("EmptyLocation", func(t *testing.T) {
		location := []float64{}
		expected := ""
		result := LocationToString(location)
		assert.Equal(t, expected, result)
	})

}

package main

import (
	"fmt"
	"strings"
)

func main() {
	query := "https://www.google.com/maps/place/Your+Business/@xx.xxxx,yy.yyyy,17z"
	
	// Handle problematic URL patterns
	if strings.Contains(query, "https://www.google.com/maps/place/Your+Business/@xx.xxxx,yy.yyyy,17z") {
		fmt.Println("WARNING: Detected template URL. Replacing with a simple business search.")
		query = "business"
	}
	
	fmt.Printf("Final query: %s\n", query)
} 
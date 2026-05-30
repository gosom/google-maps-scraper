package cli

import "fmt"

// PrintBanner displays a CLI banner with the given title.
func PrintBanner(title string) {
	fmt.Printf(`
╔═══════════════════════════════════════════════════════════╗
║%s║
╚═══════════════════════════════════════════════════════════╝
`, centerText(title, 59))
}

// PrintStep displays a numbered step message.
func PrintStep(step int, message string) {
	fmt.Printf("\n--- Step %d: %s ---\n", step, message)
}

// PrintSuccess displays a success message with a checkmark.
func PrintSuccess(message string) {
	fmt.Printf("✓ %s\n", message)
}

// PrintError displays an error message with an X.
func PrintError(message string) {
	fmt.Printf("✗ %s\n", message)
}

// PrintWarning displays a warning message.
func PrintWarning(message string) {
	fmt.Printf("⚠️  Warning: %s\n", message)
}

// centerText centers a string within the given width.
func centerText(s string, width int) string {
	if len(s) >= width {
		return s
	}

	padding := (width - len(s)) / 2

	return fmt.Sprintf("%*s%s%*s", padding, "", s, width-len(s)-padding, "")
}

// PrintCredentials prints credentials in a formatted box.
func PrintCredentials(username, password, apiKey string, userCreated bool) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════╗")
	fmt.Println("║              Credentials (SAVE THESE!)                    ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Username: %-46s ║\n", username)
	fmt.Printf("║  Password: %-46s ║\n", password)

	if apiKey != "" {
		fmt.Printf("║  API Key:  %-46s ║\n", apiKey)
	}

	fmt.Println("╚═══════════════════════════════════════════════════════════╝")
	fmt.Println()

	if userCreated {
		fmt.Println("WARNING: The password and API key are shown only once!")
		fmt.Println("         Store them securely now.")
		fmt.Println()
	}
}

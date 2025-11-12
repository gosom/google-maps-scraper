package images

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

// Type aliases to avoid circular imports while maintaining consistency
type BusinessImage struct {
	URL          string          `json:"url"`
	ThumbnailURL string          `json:"thumbnail_url,omitempty"`
	AltText      string          `json:"alt_text"`
	Category     string          `json:"category"`
	Index        int             `json:"index"`
	Dimensions   ImageDimensions `json:"dimensions,omitempty"`
	Attribution  string          `json:"attribution,omitempty"`
}

type ImageDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ScrapingMetadata struct {
	ScrapedAt     time.Time `json:"scraped_at"`
	ImageCount    int       `json:"image_count"`
	LoadTime      int       `json:"load_time_ms"`
	ScrollActions int       `json:"scroll_actions"`
}

// WaitStrategy defines parameters for waiting for dynamic content
type WaitStrategy struct {
	MaxWaitTime    time.Duration
	RetryInterval  time.Duration
	ExpectedCount  int
	ScrollAttempts int
}

// ImageExtractor handles dynamic image extraction from Google Maps
type ImageExtractor struct {
	page         playwright.Page
	waitStrategy *WaitStrategy
	metadata     *ScrapingMetadata
}

// NewImageExtractor creates a new image extractor instance
func NewImageExtractor(page playwright.Page) *ImageExtractor {
	return &ImageExtractor{
		page: page,
		waitStrategy: &WaitStrategy{
			MaxWaitTime:    30 * time.Second,
			RetryInterval:  2 * time.Second,
			ExpectedCount:  10, // Minimum expected images
			ScrollAttempts: 5,
		},
		metadata: &ScrapingMetadata{
			ScrapedAt: time.Now(),
		},
	}
}

// ExtractAllImages extracts all business images using optimized multi-method approach
func (e *ImageExtractor) ExtractAllImages(ctx context.Context) ([]BusinessImage, error) {
	// Try the new optimized approach first
	optimized := NewOptimizedImageExtractor(e.page)
	images, metadata, err := optimized.ExtractAllImagesOptimized(ctx)

	if err != nil || len(images) < 5 {
		// If optimized extraction fails or returns too few images, try legacy
		fmt.Printf("Warning: Optimized extraction insufficient (%d images), trying legacy: %v\n", len(images), err)
		return e.extractAllImagesLegacy(ctx)
	}

	// Update our metadata
	e.metadata = metadata

	// DEBUG: Log extraction results
	fmt.Printf("DEBUG: Optimized extraction found %d images\n", len(images))
	if len(images) > 0 {
		fmt.Printf("DEBUG: First image URL: %s\n", images[0].URL)
		fmt.Printf("DEBUG: First image category: %s\n", images[0].Category)

		// Show category breakdown
		categoryCount := make(map[string]int)
		for _, img := range images {
			categoryCount[img.Category]++
		}
		fmt.Printf("DEBUG: Images per category: %+v\n", categoryCount)
	}

	return images, nil
}

// extractAllImagesLegacy provides the original extraction method as complete fallback
func (e *ImageExtractor) extractAllImagesLegacy(ctx context.Context) ([]BusinessImage, error) {
	startTime := time.Now()

	// DEBUG: Log extraction start
	fmt.Printf("DEBUG: Legacy tab-based image extraction starting...\n")

	// Step 1: Navigate to images section first
	if err := e.navigateToImagesSection(); err != nil {
		fmt.Printf("Warning: Could not navigate to images section: %v\n", err)
	}

	// Step 2: Extract images by navigating through each category tab
	images, scrollActions, err := e.extractImagesByCategory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to extract images by category: %w", err)
	}

	e.metadata.ScrollActions = scrollActions
	fmt.Printf("DEBUG: Completed %d scroll actions across all categories\n", scrollActions)

	// DEBUG: Log extraction results
	fmt.Printf("DEBUG: Legacy tab-based extraction found %d images\n", len(images))
	if len(images) > 0 {
		fmt.Printf("DEBUG: First image URL: %s\n", images[0].URL)
		fmt.Printf("DEBUG: First image category: %s\n", images[0].Category)

		// Show category breakdown
		categoryCount := make(map[string]int)
		for _, img := range images {
			categoryCount[img.Category]++
		}
		fmt.Printf("DEBUG: Images per category: %+v\n", categoryCount)
	}

	// Step 3: Update metadata
	e.metadata.ImageCount = len(images)
	e.metadata.LoadTime = int(time.Since(startTime).Milliseconds())

	return images, nil
}

// loadAllImages triggers dynamic image loading via scrolling and interactions (legacy method)
func (e *ImageExtractor) loadAllImages(ctx context.Context) (int, error) {
	lastImageCount := 0
	scrollActions := 0
	maxRetries := e.waitStrategy.ScrollAttempts
	retryCount := 0

	// First, try to find and click on the images section
	if err := e.navigateToImagesSection(); err != nil {
		// Log the error but continue with scroll-based loading
		fmt.Printf("Warning: Could not navigate to images section: %v\n", err)
	}

	for retryCount < maxRetries {
		select {
		case <-ctx.Done():
			return scrollActions, ctx.Err()
		default:
		}

		// Scroll to trigger lazy loading
		_, err := e.page.Evaluate(`() => {
			// Scroll the main page
			window.scrollTo(0, document.body.scrollHeight);
			
			// Also scroll image containers if they exist
			const imageContainers = document.querySelectorAll('div[data-value="images"]');
			imageContainers.forEach(container => {
				container.scrollTo(0, container.scrollHeight);
			});
			
			// Trigger any lazy loading
			const images = document.querySelectorAll('img[data-src], img[loading="lazy"]');
			images.forEach(img => {
				if (img.dataset.src) {
					img.src = img.dataset.src;
				}
			});
		}`)

		if err != nil {
			return scrollActions, fmt.Errorf("scroll action failed: %w", err)
		}

		scrollActions++

		// Wait for new images to load
		time.Sleep(e.waitStrategy.RetryInterval)

		// Check if new images loaded
		currentCount, err := e.page.Locator("img[src*='googleusercontent'], img[src*='gstatic']").Count()
		if err != nil {
			return scrollActions, fmt.Errorf("failed to count images: %w", err)
		}

		if currentCount == lastImageCount {
			retryCount++
		} else {
			retryCount = 0
			lastImageCount = currentCount
		}

		// If we have enough images, we can stop early
		if currentCount >= e.waitStrategy.ExpectedCount {
			break
		}
	}

	return scrollActions, nil
}

// extractImagesByCategory navigates through each image tab and extracts all photos
func (e *ImageExtractor) extractImagesByCategory(ctx context.Context) ([]BusinessImage, int, error) {
	var allImages []BusinessImage
	totalScrollActions := 0

	// Step 1: Find all available tabs
	tabs, err := e.findImageTabs()
	if err != nil {
		fmt.Printf("DEBUG: Failed to find image tabs, falling back to general extraction: %v\n", err)
		return e.fallbackImageExtraction(ctx)
	}

	fmt.Printf("DEBUG: Found %d image tabs to process\n", len(tabs))

	// Step 2: Process each tab (limit to first 3 tabs and allow partial success)
	maxTabs := 3 // Further reduced to 3 for better success rate
	if len(tabs) > maxTabs {
		fmt.Printf("DEBUG: Limiting processing to first %d tabs (out of %d) for better success rate\n", maxTabs, len(tabs))
		tabs = tabs[:maxTabs]
	}

	// Create a more generous context for each tab
	tabTimeout := time.Duration(len(tabs)) * 25 * time.Second // 25s per tab
	tabCtx, tabCancel := context.WithTimeout(ctx, tabTimeout)
	defer tabCancel()

	for i, tab := range tabs {
		select {
		case <-tabCtx.Done():
			fmt.Printf("DEBUG: Context cancelled, but returning %d images collected so far\n", len(allImages))
			return allImages, totalScrollActions, nil // Return partial success
		default:
		}

		fmt.Printf("DEBUG: Processing tab %d: %s\n", i, tab.Name)

		// Click the tab to activate it (with error tolerance)
		if err := e.clickTab(tab); err != nil {
			fmt.Printf("Warning: Failed to click tab %s: %v (continuing with next tab)\n", tab.Name, err)
			continue
		}

		// Wait for tab content to load
		time.Sleep(800 * time.Millisecond) // Reduced further

		// Extract images from this tab with individual timeout
		tabImages, scrollActions, err := e.extractImagesFromCurrentTabWithTimeout(tabCtx, tab.Name, 20*time.Second)
		if err != nil {
			fmt.Printf("Warning: Failed to extract images from tab %s: %v (continuing with next tab)\n", tab.Name, err)
			continue // Don't fail the entire job, just skip this tab
		}

		fmt.Printf("DEBUG: Extracted %d images from tab %s\n", len(tabImages), tab.Name)
		allImages = append(allImages, tabImages...)
		totalScrollActions += scrollActions

		// Small delay between tabs
		time.Sleep(200 * time.Millisecond)

		// If we have some images, consider it a success
		if len(allImages) >= 10 {
			fmt.Printf("DEBUG: Collected %d images, stopping early for success\n", len(allImages))
			break
		}
	}

	fmt.Printf("DEBUG: Total images collected from tab processing: %d\n", len(allImages))
	return allImages, totalScrollActions, nil
}

// extractImagesFromCurrentTabWithTimeout extracts images with individual timeout
func (e *ImageExtractor) extractImagesFromCurrentTabWithTimeout(ctx context.Context, categoryName string, timeout time.Duration) ([]BusinessImage, int, error) {
	tabCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return e.extractImagesFromCurrentTab(tabCtx, categoryName)
}

// ImageTab represents a tab in the image gallery
type ImageTab struct {
	Name     string
	Index    int
	Element  playwright.Locator
	Selected bool
}

// findImageTabs discovers all available image category tabs
func (e *ImageExtractor) findImageTabs() ([]ImageTab, error) {
	// Look for tab buttons in the image gallery
	tabSelectors := []string{
		`button[role="tab"]`,
		`button[data-tab-index]`,
		`.hh2c6`, // Google Maps specific tab class
	}

	var tabs []ImageTab

	for _, selector := range tabSelectors {
		tabElements, err := e.page.Locator(selector).All()
		if err != nil {
			continue
		}

		if len(tabElements) > 0 {
			fmt.Printf("DEBUG: Found %d tabs using selector %s\n", len(tabElements), selector)

			for i, element := range tabElements {
				// Extract tab name
				tabName, err := e.extractTabName(element)
				if err != nil || tabName == "" {
					tabName = fmt.Sprintf("Tab_%d", i)
				}

				// Check if tab is currently selected
				selected, _ := element.GetAttribute("aria-selected")
				isSelected := selected == "true"

				tabs = append(tabs, ImageTab{
					Name:     tabName,
					Index:    i,
					Element:  element,
					Selected: isSelected,
				})
			}
			break // Use the first selector that finds tabs
		}
	}

	if len(tabs) == 0 {
		return nil, fmt.Errorf("no image tabs found")
	}

	return tabs, nil
}

// extractTabName extracts the display name from a tab element
func (e *ImageExtractor) extractTabName(element playwright.Locator) (string, error) {
	// Try different ways to get the tab name
	selectors := []string{
		`.Gpq6kf.NlVald`, // Google Maps specific text class
		`[data-value]`,   // data-value attribute
		`span`,           // Any span text
		`.tab-text`,      // Generic tab text class
	}

	for _, selector := range selectors {
		textElement := element.Locator(selector).First()
		if text, err := textElement.TextContent(); err == nil && text != "" {
			return strings.TrimSpace(text), nil
		}
	}

	// Fallback: get aria-label or title
	if ariaLabel, err := element.GetAttribute("aria-label"); err == nil && ariaLabel != "" {
		return ariaLabel, nil
	}

	if title, err := element.GetAttribute("title"); err == nil && title != "" {
		return title, nil
	}

	return "", fmt.Errorf("could not extract tab name")
}

// clickTab clicks on a specific tab to activate it
func (e *ImageExtractor) clickTab(tab ImageTab) error {
	if tab.Selected {
		fmt.Printf("DEBUG: Tab %s already selected, skipping click\n", tab.Name)
		return nil
	}

	// Check if tab is visible and clickable
	visible, err := tab.Element.IsVisible()
	if err != nil || !visible {
		return fmt.Errorf("tab %s is not visible", tab.Name)
	}

	// Try to click the tab
	if err := tab.Element.Click(); err != nil {
		return fmt.Errorf("failed to click tab %s: %w", tab.Name, err)
	}

	fmt.Printf("DEBUG: Successfully clicked tab %s\n", tab.Name)
	return nil
}

// navigateToImagesSection tries to navigate to the images section of the business page
func (e *ImageExtractor) navigateToImagesSection() error {
	// Try to find and click the "Photos" or "Images" button/tab
	selectors := []string{
		`button[data-value="images"]`,
		`[role="tab"]:has-text("Photos")`,
		`[role="tab"]:has-text("Images")`,
		`button:has-text("Photos")`,
		`button:has-text("View all")`,
		`div[data-value="images"]`,
	}

	for _, selector := range selectors {
		element := e.page.Locator(selector).First()

		// Check if element exists and is visible
		visible, err := element.IsVisible()
		if err != nil || !visible {
			continue
		}

		// Try to click it
		if err := element.Click(); err != nil {
			continue
		}

		// Wait a bit for the images section to load
		e.page.WaitForTimeout(1000)

		return nil
	}

	return fmt.Errorf("could not find images section")
}

// extractImagesFromCurrentTab extracts all images from the currently active tab
func (e *ImageExtractor) extractImagesFromCurrentTab(ctx context.Context, categoryName string) ([]BusinessImage, int, error) {
	// Step 1: Scroll to load all images in this tab
	scrollActions, err := e.loadImagesInCurrentTab(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to load images in tab: %w", err)
	}

	// Step 2: Wait for images to be available (shorter wait)
	time.Sleep(500 * time.Millisecond) // Reduced from 1000ms

	// Step 3: Extract photos from the gallery
	images, err := e.extractPhotosFromGallery(ctx, categoryName)
	if err != nil {
		return nil, scrollActions, fmt.Errorf("failed to extract photos from gallery: %w", err)
	}

	return images, scrollActions, nil
}

// loadImagesInCurrentTab scrolls within the current tab to load all available images
func (e *ImageExtractor) loadImagesInCurrentTab(ctx context.Context) (int, error) {
	scrollActions := 0
	maxScrollAttempts := 3 // Reduced from 5 to 3 for faster processing
	lastImageCount := 0
	stableCount := 0

	for scrollActions < maxScrollAttempts {
		select {
		case <-ctx.Done():
			return scrollActions, ctx.Err()
		default:
		}

		// Scroll within the current tab/gallery
		_, err := e.page.Evaluate(`() => {
			// Scroll the main gallery container
			const galleryContainers = document.querySelectorAll('div[role="main"], .m6QErb.DxyBCb, .gallery-container');
			galleryContainers.forEach(container => {
				if (container.scrollHeight > container.clientHeight) {
					container.scrollTop = container.scrollHeight;
				}
			});
			
			// Also scroll the page
			window.scrollBy(0, 500);
			
			// Trigger lazy loading for any images with data-src
			const lazyImages = document.querySelectorAll('img[data-src], [data-photo-index]');
			lazyImages.forEach(img => {
				if (img.dataset.src && !img.src) {
					img.src = img.dataset.src;
				}
			});
		}`)

		if err != nil {
			return scrollActions, fmt.Errorf("scroll action failed: %w", err)
		}

		scrollActions++

		// Wait for new images to potentially load
		time.Sleep(1000 * time.Millisecond) // Reduced from 1500ms to 1000ms

		// Check if new images appeared
		currentCount, err := e.page.Locator(`a[data-photo-index], img[src*="googleusercontent"]`).Count()
		if err != nil {
			return scrollActions, fmt.Errorf("failed to count images: %w", err)
		}

		if currentCount == lastImageCount {
			stableCount++
			if stableCount >= 2 { // If count is stable for 2 iterations, stop
				break
			}
		} else {
			stableCount = 0
			lastImageCount = currentCount
		}
	}

	return scrollActions, nil
}

// extractPhotosFromGallery extracts all photos from the current gallery view with improved error handling
func (e *ImageExtractor) extractPhotosFromGallery(ctx context.Context, categoryName string) ([]BusinessImage, error) {
	// Look for photo gallery elements with multiple selectors for robustness
	photoSelectors := []string{
		`a[data-photo-index]`,
		`div[data-photo-index]`,
		`button[data-photo-index]`,
		`.photo-item`,
		`.gallery-item`,
	}

	var photoElements []playwright.Locator
	for _, selector := range photoSelectors {
		if elements, err := e.page.Locator(selector).All(); err == nil && len(elements) > 0 {
			photoElements = elements
			fmt.Printf("DEBUG: Found %d photo elements using selector %s in %s tab\n", len(elements), selector, categoryName)
			break
		}
	}

	if len(photoElements) == 0 {
		return nil, fmt.Errorf("no photo elements found in %s tab", categoryName)
	}

	// Limit the number of photos to process for performance
	maxPhotos := 50
	if len(photoElements) > maxPhotos {
		photoElements = photoElements[:maxPhotos]
		fmt.Printf("DEBUG: Limiting to first %d photos for performance\n", maxPhotos)
	}

	var images []BusinessImage
	var mu sync.Mutex
	var wg sync.WaitGroup
	successCount := 0
	failureCount := 0

	// Process photos concurrently but with limits
	semaphore := make(chan struct{}, 3) // Reduced concurrency for stability

	for i, photoElement := range photoElements {
		wg.Add(1)

		go func(index int, element playwright.Locator) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			}

			// Add individual timeout for photo extraction
			photoCtx, photoCancel := context.WithTimeout(ctx, 5*time.Second)
			defer photoCancel()

			img, err := e.extractPhotoFromElementWithTimeout(photoCtx, element, index, categoryName)
			if err != nil {
				mu.Lock()
				failureCount++
				// Only log first few failures to avoid spam
				if failureCount <= 5 {
					fmt.Printf("Warning: Failed to extract photo %d from %s: %v\n", index, categoryName, err)
				}
				mu.Unlock()
				return
			}

			if img != nil {
				mu.Lock()
				images = append(images, *img)
				successCount++
				mu.Unlock()
			}
		}(i, photoElement)
	}

	wg.Wait()

	fmt.Printf("DEBUG: Extracted %d images from tab %s (successes: %d, failures: %d)\n", len(images), categoryName, successCount, failureCount)

	// Return success even if some photos failed, as long as we got some images
	if len(images) == 0 && failureCount > 0 {
		return nil, fmt.Errorf("failed to extract any photos from %s tab (%d failures)", categoryName, failureCount)
	}

	return images, nil
}

// extractPhotoFromElement extracts image data from a single photo gallery element
func (e *ImageExtractor) extractPhotoFromElement(element playwright.Locator, index int, categoryName string) (*BusinessImage, error) {
	return e.extractPhotoFromElementWithTimeout(context.Background(), element, index, categoryName)
}

// extractPhotoFromElementWithTimeout extracts image data with timeout handling
func (e *ImageExtractor) extractPhotoFromElementWithTimeout(ctx context.Context, element playwright.Locator, index int, categoryName string) (*BusinessImage, error) {
	// Get the photo index with error tolerance
	photoIndex, err := element.GetAttribute("data-photo-index")
	if err != nil {
		photoIndex = fmt.Sprintf("%d", index)
	}

	// Get aria-label for additional context (non-blocking)
	ariaLabel, _ := element.GetAttribute("aria-label")

	// Try multiple methods to extract the image URL with timeout
	type result struct {
		url    string
		method string
		err    error
	}

	resultChan := make(chan result, 1)
	go func() {
		url, method, err := e.extractImageURLWithMethods(element)
		resultChan <- result{url: url, method: method, err: err}
	}()

	var imageURL, method string
	var extractErr error

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("extraction timeout for photo %d in %s", index, categoryName)
	case res := <-resultChan:
		imageURL, method, extractErr = res.url, res.method, res.err
	}

	if extractErr != nil {
		return nil, fmt.Errorf("failed to extract image URL: %w", extractErr)
	}

	if imageURL == "" {
		return nil, fmt.Errorf("no image URL found")
	}

	// Log successful extraction for debugging (first success only)
	if index == 0 {
		fmt.Printf("DEBUG: Successfully extracted URL using %s: %s\n", method, imageURL[:min(80, len(imageURL))])
	}

	// Generate full-resolution URL
	fullResURL := e.generateFullResolutionURL(imageURL)

	// Parse dimensions from URL (with error tolerance)
	dimensions := parseImageDimensionsWithFallback(fullResURL, element)

	return &BusinessImage{
		URL:          fullResURL,
		ThumbnailURL: generateThumbnailURL(fullResURL),
		AltText:      ariaLabel,
		Category:     strings.ToLower(categoryName),
		Index:        parseIntFromString(photoIndex),
		Dimensions:   dimensions,
		Attribution:  fmt.Sprintf("Photo %s from %s (method: %s)", photoIndex, categoryName, method),
	}, nil
}

// parseImageDimensionsWithFallback parses dimensions with error handling
func parseImageDimensionsWithFallback(src string, locator playwright.Locator) ImageDimensions {
	// Try the original method first
	if dims := parseImageDimensions(src, locator); dims.Width > 0 || dims.Height > 0 {
		return dims
	}

	// Fallback: use default dimensions for Google images
	if strings.Contains(src, "googleusercontent.com") {
		return ImageDimensions{
			Width:  1920,
			Height: 1080,
		}
	}

	return ImageDimensions{Width: 800, Height: 600} // Safe default
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractImageURLWithMethods tries multiple methods to extract image URL and returns the method used
func (e *ImageExtractor) extractImageURLWithMethods(element playwright.Locator) (string, string, error) {
	// Method 1: Check the element itself first
	if url := e.tryExtractURLFromElement(element); url != "" {
		return url, "direct-element", nil
	}

	// Method 2: Look for specific Google Maps image containers with updated selectors
	containerSelectors := []string{
		`.U39Pmb`,                            // Primary Google Maps photo container
		`.Uf0tqf`,                            // Loaded image container
		`.YQ4gaf`,                            // Updated Google Maps container
		`.gallery-image`,                     // Generic gallery image
		`div[role="img"]`,                    // Generic image role
		`div[style*="background-image"]`,     // Any div with background-image
		`img[src*="googleusercontent"]`,      // Direct img with Google URL
		`img[data-src*="googleusercontent"]`, // Lazy loaded Google images
		`img`,                                // Regular img tags
		`div`,                                // Generic div as fallback
	}

	for _, selector := range containerSelectors {
		childElements, err := element.Locator(selector).All()
		if err != nil {
			continue
		}

		// Limit the number of child elements to check for performance
		maxChildren := 5
		if len(childElements) > maxChildren {
			childElements = childElements[:maxChildren]
		}

		for _, child := range childElements {
			if url := e.tryExtractURLFromElement(child); url != "" {
				return url, fmt.Sprintf("child-%s", selector), nil
			}
		}
	}

	// Method 3: Try to extract from onclick or data attributes
	if url, err := e.extractFromDataAttributes(element); err == nil && url != "" {
		return url, "data-attributes", nil
	}

	// Method 4: Try to extract from any nested img src attributes
	if url, err := e.extractFromNestedImages(element); err == nil && url != "" {
		return url, "nested-img", nil
	}

	// Method 5: Try to extract from parent element attributes
	if url, err := e.extractFromParentElements(element); err == nil && url != "" {
		return url, "parent-element", nil
	}

	return "", "none", fmt.Errorf("no valid image URL found using any method")
}

// extractFromDataAttributes tries to extract URLs from data attributes
func (e *ImageExtractor) extractFromDataAttributes(element playwright.Locator) (string, error) {
	dataAttrs := []string{"data-src", "data-original", "data-lazy", "data-image", "data-url"}

	for _, attr := range dataAttrs {
		if value, err := element.GetAttribute(attr); err == nil && value != "" {
			if isValidGoogleImageURL(value) {
				return value, nil
			}
		}
	}

	return "", fmt.Errorf("no valid data attribute URL found")
}

// extractFromNestedImages looks for img tags within the element
func (e *ImageExtractor) extractFromNestedImages(element playwright.Locator) (string, error) {
	imgElements, err := element.Locator("img").All()
	if err != nil {
		return "", err
	}

	// Limit number of nested images to check for performance
	maxImages := 3
	if len(imgElements) > maxImages {
		imgElements = imgElements[:maxImages]
	}

	for _, img := range imgElements {
		// Try src attribute first
		if src, err := img.GetAttribute("src"); err == nil && src != "" {
			if isValidGoogleImageURL(src) {
				return src, nil
			}
		}

		// Try data-src for lazy loaded images
		if dataSrc, err := img.GetAttribute("data-src"); err == nil && dataSrc != "" {
			if isValidGoogleImageURL(dataSrc) {
				return dataSrc, nil
			}
		}

		// Try data-original for other lazy loading implementations
		if dataOriginal, err := img.GetAttribute("data-original"); err == nil && dataOriginal != "" {
			if isValidGoogleImageURL(dataOriginal) {
				return dataOriginal, nil
			}
		}
	}

	return "", fmt.Errorf("no valid nested image URL found")
}

// extractFromParentElements looks for image URLs in parent element attributes
func (e *ImageExtractor) extractFromParentElements(element playwright.Locator) (string, error) {
	// Get parent element and check its attributes
	parent := element.Locator("..").First()

	// Check parent's style attribute for background-image
	if style, err := parent.GetAttribute("style"); err == nil && style != "" {
		if url := e.extractURLFromStyle(style); url != "" {
			return url, nil
		}
	}

	// Check parent's data attributes
	dataAttrs := []string{"data-src", "data-image", "data-url", "data-photo"}
	for _, attr := range dataAttrs {
		if value, err := parent.GetAttribute(attr); err == nil && value != "" {
			if isValidGoogleImageURL(value) {
				return value, nil
			}
		}
	}

	return "", fmt.Errorf("no valid parent element URL found")
}

// extractImageURLFromElement extracts the image URL from background-image style or img src
func (e *ImageExtractor) extractImageURLFromElement(element playwright.Locator) (string, error) {
	// Look for background-image in various child elements
	selectors := []string{
		`.U39Pmb`, // Google Maps photo container
		`.Uf0tqf`, // Google Maps loaded image
		`div[role="img"]`,
		`img`,
		`div`, // Generic div that might have background-image
	}

	// First, try to extract from the element itself
	if url := e.tryExtractURLFromElement(element); url != "" {
		return url, nil
	}

	// Then try child elements
	for _, selector := range selectors {
		childElement := element.Locator(selector).First()
		if url := e.tryExtractURLFromElement(childElement); url != "" {
			return url, nil
		}
	}

	return "", fmt.Errorf("no valid image URL found")
}

// tryExtractURLFromElement attempts to extract URL from a single element
func (e *ImageExtractor) tryExtractURLFromElement(element playwright.Locator) string {
	// Method 1: Try background-image from style attribute
	if style, err := element.GetAttribute("style"); err == nil && style != "" {
		if url := e.extractURLFromStyle(style); url != "" {
			return url
		}
	}

	// Method 2: Try src attribute for img elements
	if src, err := element.GetAttribute("src"); err == nil && src != "" {
		if isValidGoogleImageURL(src) {
			return src
		}
	}

	// Method 3: Try data-src attribute for lazy-loaded images
	if dataSrc, err := element.GetAttribute("data-src"); err == nil && dataSrc != "" {
		if isValidGoogleImageURL(dataSrc) {
			return dataSrc
		}
	}

	// Method 4: Try to get computed background-image style
	if computedStyle, err := element.Evaluate(`el => window.getComputedStyle(el).backgroundImage`, nil); err == nil {
		if styleStr, ok := computedStyle.(string); ok && styleStr != "" && styleStr != "none" {
			if url := e.extractURLFromStyle(styleStr); url != "" {
				return url
			}
		}
	}

	return ""
}

// extractURLFromStyle extracts URL from CSS background-image style
func (e *ImageExtractor) extractURLFromStyle(style string) string {
	if !strings.Contains(style, "url(") {
		return ""
	}

	// Extract URL from background-image: url("...")
	if start := strings.Index(style, "url("); start != -1 {
		start += 4

		// Skip opening quote/entity if present
		if start < len(style) {
			if strings.HasPrefix(style[start:], "&quot;") {
				start += 6 // Skip &quot;
			} else if style[start] == '"' || style[start] == '\'' {
				start++
			}
		}

		if end := strings.Index(style[start:], ")"); end != -1 {
			url := style[start : start+end]

			// Remove &quot; at the end if present
			if strings.HasSuffix(url, "&quot;") {
				url = url[:len(url)-6]
			}

			// Clean up HTML entities
			url = strings.ReplaceAll(url, "&quot;", "")
			url = strings.ReplaceAll(url, "&amp;", "&")
			url = strings.ReplaceAll(url, "&lt;", "<")
			url = strings.ReplaceAll(url, "&gt;", ">")

			// Remove closing quote if present
			if len(url) > 0 && (url[len(url)-1] == '"' || url[len(url)-1] == '\'') {
				url = url[:len(url)-1]
			}

			if isValidGoogleImageURL(url) {
				return url
			}
		}
	}

	return ""
}

// generateFullResolutionURL converts a thumbnail URL to a full-resolution URL
func (e *ImageExtractor) generateFullResolutionURL(originalURL string) string {
	if !strings.Contains(originalURL, "googleusercontent.com") {
		return originalURL
	}

	// Remove size parameters and add high-resolution parameters
	if strings.Contains(originalURL, "=w") {
		parts := strings.Split(originalURL, "=w")
		if len(parts) > 1 {
			// Use larger dimensions for full resolution
			return parts[0] + "=w1920-h1080-k-no"
		}
	}

	// If no size parameters, add full resolution parameters
	return originalURL + "=w1920-h1080-k-no"
}

// fallbackImageExtraction provides fallback when tab-based extraction fails
func (e *ImageExtractor) fallbackImageExtraction(ctx context.Context) ([]BusinessImage, int, error) {
	fmt.Printf("DEBUG: Using fallback image extraction method\n")

	// Use the original extraction method as fallback
	scrollActions, err := e.loadAllImages(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("fallback loading failed: %w", err)
	}

	if err := e.waitForImages(ctx); err != nil {
		return nil, scrollActions, fmt.Errorf("fallback waiting failed: %w", err)
	}

	images, err := e.extractImagesFromDOM(ctx)
	if err != nil {
		return nil, scrollActions, fmt.Errorf("fallback extraction failed: %w", err)
	}

	return images, scrollActions, nil
}

// waitForImages waits for images to be available in the DOM
func (e *ImageExtractor) waitForImages(ctx context.Context) error {
	// Wait for initial images to appear
	_, err := e.page.WaitForSelector("img[src*='googleusercontent']", playwright.PageWaitForSelectorOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(15000),
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for initial images: %w", err)
	}

	// Wait for target number of images with additional checks
	deadline := time.Now().Add(e.waitStrategy.MaxWaitTime)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		currentCount, err := e.page.Locator("img[src*='googleusercontent'], img[src*='gstatic']").Count()
		if err != nil {
			return fmt.Errorf("failed to count images: %w", err)
		}

		if currentCount >= e.waitStrategy.ExpectedCount {
			// Wait for network to be idle to ensure all images loaded
			err := e.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State: playwright.LoadStateNetworkidle,
			})
			if err != nil {
				// Don't fail on network idle timeout, just continue
				fmt.Printf("Warning: Network idle timeout: %v\n", err)
			}
			return nil
		}

		time.Sleep(e.waitStrategy.RetryInterval)
	}

	// Don't fail if we don't reach expected count - return what we have
	return nil
}

// extractImagesFromDOM extracts all images from the current DOM
func (e *ImageExtractor) extractImagesFromDOM(ctx context.Context) ([]BusinessImage, error) {
	imageLocators, err := e.page.Locator("img[src*='googleusercontent'], img[src*='gstatic']").All()
	if err != nil {
		return nil, fmt.Errorf("failed to get image locators: %w", err)
	}

	var images []BusinessImage
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Process images concurrently for better performance
	semaphore := make(chan struct{}, 10) // Limit concurrent operations

	for i, locator := range imageLocators {
		wg.Add(1)

		go func(index int, loc playwright.Locator) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			}

			img, err := e.extractSingleImage(loc, index)
			if err != nil {
				// Log error but continue processing other images
				fmt.Printf("Warning: Failed to extract image %d: %v\n", index, err)
				return
			}

			if img != nil {
				mu.Lock()
				images = append(images, *img)
				mu.Unlock()
			}
		}(i, locator)
	}

	wg.Wait()

	return images, nil
}

// extractSingleImage extracts data from a single image element
func (e *ImageExtractor) extractSingleImage(locator playwright.Locator, index int) (*BusinessImage, error) {
	// Get image source URL
	src, err := locator.GetAttribute("src")
	if err != nil || src == "" {
		return nil, fmt.Errorf("no src attribute found")
	}

	// Validate that this is a Google image URL
	if !isValidGoogleImageURL(src) {
		return nil, fmt.Errorf("invalid Google image URL: %s", src)
	}

	// Get alternative text and title
	altText, _ := locator.GetAttribute("alt")
	title, _ := locator.GetAttribute("title")

	// Parse image dimensions from URL parameters or element attributes
	dimensions := parseImageDimensions(src, locator)

	// Determine image category based on context and attributes
	category := categorizeImage(altText, title, src)

	// Generate thumbnail URL if possible
	thumbnailURL := generateThumbnailURL(src)

	return &BusinessImage{
		URL:          src,
		ThumbnailURL: thumbnailURL,
		AltText:      altText,
		Index:        index,
		Dimensions:   dimensions,
		Category:     category,
		Attribution:  title,
	}, nil
}

// parseImageDimensions extracts dimensions from URL parameters or element attributes
func parseImageDimensions(src string, locator playwright.Locator) ImageDimensions {
	dimensions := ImageDimensions{}

	// Try to extract from URL parameters (e.g., =w400-h300-k-no)
	if strings.Contains(src, "=w") {
		parts := strings.Split(src, "=w")
		if len(parts) > 1 {
			params := parts[len(parts)-1]
			if strings.Contains(params, "-h") {
				wh := strings.Split(params, "-h")
				if len(wh) >= 2 {
					// Parse width and height
					wStr := wh[0]
					hStr := strings.Split(wh[1], "-")[0]

					if w := parseIntFromString(wStr); w > 0 {
						dimensions.Width = w
					}
					if h := parseIntFromString(hStr); h > 0 {
						dimensions.Height = h
					}
				}
			}
		}
	}

	// If dimensions not found in URL, try element attributes
	if dimensions.Width == 0 || dimensions.Height == 0 {
		if width, err := locator.GetAttribute("width"); err == nil && width != "" {
			dimensions.Width = parseIntFromString(width)
		}
		if height, err := locator.GetAttribute("height"); err == nil && height != "" {
			dimensions.Height = parseIntFromString(height)
		}
	}

	return dimensions
}

// categorizeImage determines the category of an image based on its attributes and context
func categorizeImage(altText, title, src string) string {
	altLower := strings.ToLower(altText)
	titleLower := strings.ToLower(title)

	// Check for menu images
	if strings.Contains(altLower, "menu") || strings.Contains(titleLower, "menu") {
		return "menu"
	}

	// Check for user-uploaded images
	if strings.Contains(altLower, "user") || strings.Contains(titleLower, "user") ||
		strings.Contains(altLower, "customer") || strings.Contains(titleLower, "customer") {
		return "user"
	}

	// Check for street view images
	if strings.Contains(altLower, "street") || strings.Contains(titleLower, "street") ||
		strings.Contains(src, "streetview") {
		return "street"
	}

	// Default to business images
	return "business"
}

// generateThumbnailURL creates a thumbnail version of the image URL
func generateThumbnailURL(originalURL string) string {
	// For Google URLs, we can modify the size parameters
	if strings.Contains(originalURL, "googleusercontent.com") {
		// Replace size parameters with thumbnail size
		if strings.Contains(originalURL, "=w") {
			parts := strings.Split(originalURL, "=w")
			if len(parts) > 1 {
				// Replace with small thumbnail dimensions
				return parts[0] + "=w150-h150-c"
			}
		}
		// If no size parameters, add thumbnail parameters
		return originalURL + "=w150-h150-c"
	}

	return originalURL
}

// isValidGoogleImageURL checks if a URL is a valid Google image URL
func isValidGoogleImageURL(url string) bool {
	return strings.Contains(url, "googleusercontent.com") ||
		strings.Contains(url, "gstatic.com") ||
		strings.Contains(url, "googlemaps.com")
}

// parseIntFromString safely parses an integer from a string
func parseIntFromString(s string) int {
	// Remove any non-numeric characters except the first digits
	var numStr strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			numStr.WriteRune(r)
		} else if numStr.Len() > 0 {
			break // Stop at first non-digit after we've started collecting digits
		}
	}

	if numStr.Len() == 0 {
		return 0
	}

	// Simple integer parsing
	result := 0
	for _, r := range numStr.String() {
		if r >= '0' && r <= '9' {
			result = result*10 + int(r-'0')
		}
	}

	return result
}

// GetMetadata returns the scraping metadata
func (e *ImageExtractor) GetMetadata() *ScrapingMetadata {
	return e.metadata
}

// GetLinkSources provides backward compatibility with the original function signature
func GetLinkSources(page playwright.Page) ([]BusinessImage, error) {
	extractor := NewImageExtractor(page)
	return extractor.ExtractAllImages(context.Background())
}

// GetLinkSource provides backward compatibility - returns first image URL
func GetLinkSource(page playwright.Page) (string, error) {
	images, err := GetLinkSources(page)
	if err != nil {
		return "", err
	}
	if len(images) > 0 {
		return images[0].URL, nil
	}
	return "", nil
}

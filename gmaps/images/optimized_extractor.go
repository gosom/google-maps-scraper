package images

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

// OptimizedImageExtractor provides improved image extraction with better error handling
type OptimizedImageExtractor struct {
	page                   playwright.Page
	maxTimeout             time.Duration
	maxImagesPerTab        int
	useAggressiveScrolling bool
	fallbackMethods        []ExtractionMethod
}

// ExtractionMethod defines different approaches to extract images
type ExtractionMethod interface {
	Name() string
	Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error)
	Priority() int
}

// NewOptimizedImageExtractor creates an improved image extractor
func NewOptimizedImageExtractor(page playwright.Page) *OptimizedImageExtractor {
	return &OptimizedImageExtractor{
		page:                   page,
		maxTimeout:             30 * time.Second, // Optimized for fast extraction
		maxImagesPerTab:        50,               // Reasonable limit per tab
		useAggressiveScrolling: false,            // Start conservative
		fallbackMethods: []ExtractionMethod{
			&ScrollAllTabMethod{}, // NEW: Highest priority - scroll in All tab (no dialog issues!)
			&DirectGalleryMethod{},
			&TabBasedMethod{},
			&LegacyDOMMethod{},
			&AppStateMethod{},
		},
	}
}

// ExtractAllImagesOptimized performs optimized image extraction with multiple fallback strategies
func (e *OptimizedImageExtractor) ExtractAllImagesOptimized(ctx context.Context) ([]BusinessImage, *ScrapingMetadata, error) {
	startTime := time.Now()
	metadata := &ScrapingMetadata{
		ScrapedAt: startTime,
	}

	// Create extraction context with timeout
	extractCtx, cancel := context.WithTimeout(ctx, e.maxTimeout)
	defer cancel()

	// Sort methods by priority (highest first)
	sort.Slice(e.fallbackMethods, func(i, j int) bool {
		return e.fallbackMethods[i].Priority() > e.fallbackMethods[j].Priority()
	})

	var allImages []BusinessImage
	var lastError error

	// Try each extraction method until we get sufficient results
	for i, method := range e.fallbackMethods {
		select {
		case <-extractCtx.Done():
			break
		default:
		}

		fmt.Printf("DEBUG: Trying extraction method %d: %s\n", i+1, method.Name())

		// Create method-specific context with shorter timeout
		methodTimeout := time.Duration(float64(e.maxTimeout) * 0.4) // 40% of total time per method
		methodCtx, methodCancel := context.WithTimeout(extractCtx, methodTimeout)

		images, err := method.Extract(methodCtx, e.page)
		methodCancel()

		if err != nil {
			fmt.Printf("Warning: Method %s failed: %v\n", method.Name(), err)
			lastError = err

			// CRITICAL: If ScrollAllTab method fails, still stop here - don't try TabBasedMethod
			if method.Name() == "ScrollAllTab" {
				fmt.Printf("DEBUG: ScrollAllTab method failed but stopping to avoid tab clicking\n")
				break
			}
			continue
		}

		if len(images) > 0 {
			fmt.Printf("DEBUG: Method %s succeeded with %d images\n", method.Name(), len(images))
			allImages = e.mergeImages(allImages, images)

			// CRITICAL: If ScrollAllTab succeeds with ANY images, stop immediately
			if method.Name() == "ScrollAllTab" && len(allImages) > 0 {
				fmt.Printf("DEBUG: ScrollAllTab got %d images, stopping to avoid tab clicking\n", len(allImages))
				break
			}

			// If we have enough images, stop trying other methods
			if len(allImages) >= 20 {
				fmt.Printf("DEBUG: Sufficient images collected (%d), stopping extraction\n", len(allImages))
				break
			}
		}
	}

	// Update metadata
	metadata.ImageCount = len(allImages)
	metadata.LoadTime = int(time.Since(startTime).Milliseconds())

	if len(allImages) == 0 && lastError != nil {
		return nil, metadata, fmt.Errorf("all extraction methods failed, last error: %w", lastError)
	}

	fmt.Printf("DEBUG: Optimized extraction completed - %d images in %dms\n", len(allImages), metadata.LoadTime)
	return allImages, metadata, nil
}

// mergeImages combines image arrays, removing duplicates
func (e *OptimizedImageExtractor) mergeImages(existing, new []BusinessImage) []BusinessImage {
	urlMap := make(map[string]bool)
	result := make([]BusinessImage, 0, len(existing)+len(new))

	// Add existing images
	for _, img := range existing {
		if !urlMap[img.URL] {
			urlMap[img.URL] = true
			result = append(result, img)
		}
	}

	// Add new images, avoiding duplicates
	for _, img := range new {
		if !urlMap[img.URL] {
			urlMap[img.URL] = true
			result = append(result, img)
		}
	}

	return result
}

// DirectGalleryMethod tries to extract images directly from visible gallery
type DirectGalleryMethod struct{}

func (m *DirectGalleryMethod) Name() string  { return "DirectGallery" }
func (m *DirectGalleryMethod) Priority() int { return 100 }

func (m *DirectGalleryMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	// Look for directly visible images with current Google Maps selectors
	currentSelectors := []string{
		`img[src*="googleusercontent.com"]`,
		`img[src*="maps.gstatic.com"]`,
		`div[style*="googleusercontent.com"]`,
		`img[jsname]`, // Google Maps uses jsname attributes
		`img[data-src*="googleusercontent.com"]`,
	}

	var allImages []BusinessImage

	for _, selector := range currentSelectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}

		fmt.Printf("DEBUG: DirectGallery found %d elements with selector: %s\n", len(elements), selector)

		for i, element := range elements {
			select {
			case <-ctx.Done():
				return allImages, ctx.Err()
			default:
			}

			img := m.extractImageFromElement(element, i, "direct")
			if img != nil {
				allImages = append(allImages, *img)
			}

			// Limit per selector to avoid getting stuck
			if len(allImages) >= 30 {
				break
			}
		}

		// If we got images from this selector, we might have enough
		if len(allImages) >= 15 {
			break
		}
	}

	return allImages, nil
}

func (m *DirectGalleryMethod) extractImageFromElement(element playwright.Locator, index int, category string) *BusinessImage {
	// Try src attribute first
	if src, err := element.GetAttribute("src"); err == nil && src != "" {
		if m.isValidImageURL(src) {
			return m.createBusinessImage(src, index, category, element)
		}
	}

	// Try data-src for lazy loaded images
	if dataSrc, err := element.GetAttribute("data-src"); err == nil && dataSrc != "" {
		if m.isValidImageURL(dataSrc) {
			return m.createBusinessImage(dataSrc, index, category, element)
		}
	}

	// Try background-image from style
	if style, err := element.GetAttribute("style"); err == nil && style != "" {
		if url := m.extractURLFromStyle(style); url != "" && m.isValidImageURL(url) {
			return m.createBusinessImage(url, index, category, element)
		}
	}

	return nil
}

func (m *DirectGalleryMethod) isValidImageURL(url string) bool {
	return strings.Contains(url, "googleusercontent.com") ||
		strings.Contains(url, "gstatic.com") ||
		strings.Contains(url, "googlemaps.com")
}

func (m *DirectGalleryMethod) extractURLFromStyle(style string) string {
	if start := strings.Index(style, "url("); start != -1 {
		start += 4
		if strings.HasPrefix(style[start:], `"`) || strings.HasPrefix(style[start:], `'`) {
			start++
		}

		if end := strings.IndexAny(style[start:], `"')`); end != -1 {
			return style[start : start+end]
		}
	}
	return ""
}

func (m *DirectGalleryMethod) createBusinessImage(url string, index int, category string, element playwright.Locator) *BusinessImage {
	// Get additional attributes
	altText, _ := element.GetAttribute("alt")
	title, _ := element.GetAttribute("title")

	// Enhance URL for full resolution
	fullURL := m.enhanceImageURL(url)

	return &BusinessImage{
		URL:          fullURL,
		ThumbnailURL: m.createThumbnailURL(fullURL),
		AltText:      altText,
		Category:     category,
		Index:        index,
		Attribution:  title,
		Dimensions:   m.parseImageDimensions(url),
	}
}

func (m *DirectGalleryMethod) enhanceImageURL(url string) string {
	if strings.Contains(url, "googleusercontent.com") {
		if strings.Contains(url, "=w") {
			parts := strings.Split(url, "=w")
			if len(parts) > 1 {
				return parts[0] + "=w1920-h1080-k-no"
			}
		} else {
			return url + "=w1920-h1080-k-no"
		}
	}
	return url
}

func (m *DirectGalleryMethod) createThumbnailURL(url string) string {
	if strings.Contains(url, "googleusercontent.com") {
		if strings.Contains(url, "=w") {
			parts := strings.Split(url, "=w")
			if len(parts) > 1 {
				return parts[0] + "=w200-h200-c"
			}
		} else {
			return url + "=w200-h200-c"
		}
	}
	return url
}

func (m *DirectGalleryMethod) parseImageDimensions(url string) ImageDimensions {
	// Extract dimensions from URL parameters
	if strings.Contains(url, "=w") && strings.Contains(url, "-h") {
		parts := strings.Split(url, "=w")
		if len(parts) > 1 {
			params := parts[1]
			if hIndex := strings.Index(params, "-h"); hIndex != -1 {
				wStr := params[:hIndex]
				hPart := params[hIndex+2:]
				if dashIndex := strings.Index(hPart, "-"); dashIndex != -1 {
					hStr := hPart[:dashIndex]
					return ImageDimensions{
						Width:  m.parseInt(wStr),
						Height: m.parseInt(hStr),
					}
				}
			}
		}
	}
	return ImageDimensions{}
}

func (m *DirectGalleryMethod) parseInt(s string) int {
	result := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			result = result*10 + int(r-'0')
		} else {
			break
		}
	}
	return result
}

// TabBasedMethod uses the original tab-based approach but optimized
type TabBasedMethod struct{}

func (m *TabBasedMethod) Name() string  { return "TabBased" }
func (m *TabBasedMethod) Priority() int { return 80 }

func (m *TabBasedMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	// Try to navigate to images section first
	if err := m.navigateToImages(page); err != nil {
		return nil, fmt.Errorf("failed to navigate to images: %w", err)
	}

	// Wait briefly for images section to load
	time.Sleep(500 * time.Millisecond)

	// Find available tabs with more robust selectors
	tabs, err := m.findImageTabs(page)
	if err != nil {
		return nil, fmt.Errorf("failed to find image tabs: %w", err)
	}

	if len(tabs) == 0 {
		return nil, fmt.Errorf("no image tabs found")
	}

	var allImages []BusinessImage
	maxTabs := 2 // Process only first 2 tabs for performance

	if len(tabs) > maxTabs {
		tabs = tabs[:maxTabs]
	}

	// Process each tab with timeout
	for i, tab := range tabs {
		select {
		case <-ctx.Done():
			return allImages, ctx.Err()
		default:
		}

		fmt.Printf("DEBUG: Processing tab %d: %s\n", i, tab.Name)

		tabImages, err := m.processTab(ctx, page, tab)
		if err != nil {
			fmt.Printf("Warning: Failed to process tab %s: %v\n", tab.Name, err)
			continue
		}

		allImages = append(allImages, tabImages...)

		// Stop if we have enough images
		if len(allImages) >= 25 {
			break
		}
	}

	return allImages, nil
}

func (m *TabBasedMethod) navigateToImages(page playwright.Page) error {
	selectors := []string{
		`button[data-value="images"]`,
		`[role="tab"][aria-label*="Photo"]`,
		`[role="tab"][aria-label*="Image"]`,
		`button:has-text("Photos")`,
		`button:has-text("Images")`,
	}

	for _, selector := range selectors {
		element := page.Locator(selector).First()
		if visible, _ := element.IsVisible(); visible {
			if err := element.Click(); err == nil {
				time.Sleep(800 * time.Millisecond)
				return nil
			}
		}
	}

	return fmt.Errorf("could not navigate to images section")
}

func (m *TabBasedMethod) findImageTabs(page playwright.Page) ([]ImageTab, error) {
	selectors := []string{
		`button[role="tab"]`,
		`.hh2c6`, // Google Maps tab class
		`button[data-tab]`,
	}

	for _, selector := range selectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}

		if len(elements) > 0 {
			var tabs []ImageTab
			for i, element := range elements {
				name := m.extractTabName(element)
				if name == "" {
					name = fmt.Sprintf("Tab_%d", i)
				}

				tabs = append(tabs, ImageTab{
					Name:    name,
					Index:   i,
					Element: element,
				})
			}
			return tabs, nil
		}
	}

	return nil, fmt.Errorf("no tabs found")
}

func (m *TabBasedMethod) extractTabName(element playwright.Locator) string {
	// Try different ways to get tab name
	if text, err := element.TextContent(); err == nil && text != "" {
		return strings.TrimSpace(text)
	}

	if label, err := element.GetAttribute("aria-label"); err == nil && label != "" {
		return label
	}

	if title, err := element.GetAttribute("title"); err == nil && title != "" {
		return title
	}

	return ""
}

func (m *TabBasedMethod) processTab(ctx context.Context, page playwright.Page, tab ImageTab) ([]BusinessImage, error) {
	// Click the tab
	if err := tab.Element.Click(); err != nil {
		return nil, fmt.Errorf("failed to click tab: %w", err)
	}

	// Wait for content to load
	time.Sleep(500 * time.Millisecond)

	// Extract images with timeout
	tabCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	return m.extractImagesFromCurrentTab(tabCtx, page, tab.Name)
}

func (m *TabBasedMethod) extractImagesFromCurrentTab(ctx context.Context, page playwright.Page, category string) ([]BusinessImage, error) {
	// Minimal scrolling first
	m.performMinimalScroll(page)

	// Extract visible images using reliable selectors
	selectors := []string{
		`a[data-photo-index] img`,
		`img[src*="googleusercontent.com"]`,
		`div[role="img"]`,
	}

	var images []BusinessImage

	for _, selector := range selectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}

		for i, element := range elements {
			select {
			case <-ctx.Done():
				return images, ctx.Err()
			default:
			}

			img := m.extractImageFromElement(element, i, category)
			if img != nil {
				images = append(images, *img)
			}

			// Limit per selector
			if len(images) >= 20 {
				break
			}
		}

		if len(images) >= 15 {
			break
		}
	}

	return images, nil
}

func (m *TabBasedMethod) performMinimalScroll(page playwright.Page) {
	// Perform just a few scroll actions
	for i := 0; i < 2; i++ {
		_, _ = page.Evaluate(`() => {
			window.scrollBy(0, 300);
			const containers = document.querySelectorAll('[role="main"], .gallery-container');
			containers.forEach(c => c.scrollTop += 300);
		}`)
		time.Sleep(500 * time.Millisecond)
	}
}

func (m *TabBasedMethod) extractImageFromElement(element playwright.Locator, index int, category string) *BusinessImage {
	// Similar to DirectGalleryMethod but with tab-specific category
	if src, err := element.GetAttribute("src"); err == nil && src != "" {
		if m.isValidImageURL(src) {
			return m.createBusinessImage(src, index, category, element)
		}
	}

	return nil
}

func (m *TabBasedMethod) isValidImageURL(url string) bool {
	return strings.Contains(url, "googleusercontent.com") ||
		strings.Contains(url, "gstatic.com")
}

func (m *TabBasedMethod) createBusinessImage(url string, index int, category string, element playwright.Locator) *BusinessImage {
	altText, _ := element.GetAttribute("alt")

	return &BusinessImage{
		URL:         m.enhanceImageURL(url),
		AltText:     altText,
		Category:    strings.ToLower(category),
		Index:       index,
		Attribution: fmt.Sprintf("Tab: %s, Index: %d", category, index),
	}
}

func (m *TabBasedMethod) enhanceImageURL(url string) string {
	if strings.Contains(url, "googleusercontent.com") && !strings.Contains(url, "=w") {
		return url + "=w1920-h1080-k-no"
	}
	return url
}

// LegacyDOMMethod uses the original DOM-based extraction as fallback
type LegacyDOMMethod struct{}

func (m *LegacyDOMMethod) Name() string  { return "LegacyDOM" }
func (m *LegacyDOMMethod) Priority() int { return 60 }

func (m *LegacyDOMMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	// Use the original extractor as fallback
	extractor := NewImageExtractor(page)
	images, _, err := extractor.fallbackImageExtraction(ctx)
	return images, err
}

// AppStateMethod tries to extract from APP_INITIALIZATION_STATE
type AppStateMethod struct{}

func (m *AppStateMethod) Name() string  { return "AppState" }
func (m *AppStateMethod) Priority() int { return 40 }

func (m *AppStateMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	// This would implement APP_INITIALIZATION_STATE extraction
	// For now, return empty to maintain interface compatibility
	return []BusinessImage{}, fmt.Errorf("APP_INITIALIZATION_STATE extraction not implemented yet")
}

// ScrollAllTabMethod scrolls in "All" tab without clicking other tabs (FAST, no dialogs!)
type ScrollAllTabMethod struct{}

func (m *ScrollAllTabMethod) Name() string  { return "ScrollAllTab" }
func (m *ScrollAllTabMethod) Priority() int { return 110 } // Highest priority!

func (m *ScrollAllTabMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	fmt.Printf("DEBUG: ScrollAllTab method - FAST extraction starting\n")

	// Step 1: Try to navigate to images section (but don't wait long)
	if err := m.navigateToImages(page); err != nil {
		fmt.Printf("DEBUG: Could not navigate to photos section: %v - extracting from current page\n", err)
	} else {
		// Quick wait for gallery - reduced from 2s to 500ms
		time.Sleep(500 * time.Millisecond)
	}

	// Step 2: Fast scroll and collect (optimized for speed)
	return m.scrollAndCollectImages(ctx, page)
}

func (m *ScrollAllTabMethod) navigateToImages(page playwright.Page) error {
	selectors := []string{
		`button[data-value="images"]`,
		`[role="tab"][aria-label*="Photo"]`,
		`button:has-text("Photos")`,
		`button:has-text("View all")`,
	}

	for _, selector := range selectors {
		element := page.Locator(selector).First()
		if visible, _ := element.IsVisible(); visible {
			if err := element.Click(); err == nil {
				fmt.Printf("DEBUG: Clicked photos button\n")
				time.Sleep(800 * time.Millisecond)
				return nil
			}
		}
	}

	return fmt.Errorf("could not find photos button")
}

func (m *ScrollAllTabMethod) scrollAndCollectImages(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	urlSet := make(map[string]bool)
	var allImages []BusinessImage
	scrollCount := 0
	stableCount := 0
	maxScrolls := 10 // Reduced from 25 to 10 for speed
	maxStable := 2   // Reduced from 5 to 2 (less patient, faster)

	fmt.Printf("DEBUG: Starting FAST scroll in All tab...\n")

	for scrollCount < maxScrolls {
		select {
		case <-ctx.Done():
			return allImages, nil
		default:
		}

		previousCount := len(urlSet)

		// Fast scroll
		scrolled := m.scrollGallery(page)
		if !scrolled {
			fmt.Printf("DEBUG: Can't scroll anymore, stopping\n")
			break
		}

		scrollCount++

		// Minimal wait for lazy loading - reduced from 1.5s to 400ms
		time.Sleep(400 * time.Millisecond)

		// Extract visible images
		newImages := m.extractVisibleImages(page)
		for _, img := range newImages {
			if !urlSet[img.URL] {
				urlSet[img.URL] = true
				allImages = append(allImages, img)
			}
		}

		newFound := len(urlSet) - previousCount
		fmt.Printf("DEBUG: Scroll %d: +%d images (total: %d)\n", scrollCount, newFound, len(urlSet))

		if newFound == 0 {
			stableCount++
			if stableCount >= maxStable {
				fmt.Printf("DEBUG: No new images after %d scrolls, done\n", maxStable)
				break
			}
		} else {
			stableCount = 0
		}
	}

	fmt.Printf("DEBUG: ScrollAllTab completed: %d images after %d scrolls\n", len(allImages), scrollCount)
	return allImages, nil
}

func (m *ScrollAllTabMethod) scrollGallery(page playwright.Page) bool {
	scrolled, err := page.Evaluate(`() => {
		const selectors = [
			'div[role="main"]',
			'div.m6QErb.DxyBCb',
			'div[jslog]',
			'div[class*="gallery"]',
		];
		
		let scrolledAny = false;
		
		for (const sel of selectors) {
			const containers = document.querySelectorAll(sel);
			console.log('Trying selector:', sel, 'found:', containers.length);
			
			for (const c of containers) {
				const scrollHeight = c.scrollHeight;
				const clientHeight = c.clientHeight;
				const scrollTop = c.scrollTop;
				
				console.log('Container:', sel, 'scrollHeight:', scrollHeight, 'clientHeight:', clientHeight, 'scrollTop:', scrollTop);
				
					if (scrollHeight > clientHeight) {
						const before = c.scrollTop;
						c.scrollBy(0, 1200); // Increased from 800 to 1200 for even faster scrolling
						const after = c.scrollTop;
					
					console.log('Scrolled from', before, 'to', after);
					
					if (after > before) {
						scrolledAny = true;
						break;
					}
				}
			}
			if (scrolledAny) break;
		}
		
		if (!scrolledAny) {
			console.log('Trying window scroll as fallback');
			const before = window.scrollY;
			window.scrollBy(0, 1200); // Increased from 800 to 1200 for speed
			const after = window.scrollY;
			console.log('Window scrolled from', before, 'to', after);
			scrolledAny = after > before;
		}
		
		return scrolledAny;
	}`)

	if err != nil {
		fmt.Printf("DEBUG: Scroll evaluation error: %v\n", err)
		return false
	}

	if b, ok := scrolled.(bool); ok {
		if b {
			fmt.Printf("DEBUG: ✅ Scroll successful\n")
		} else {
			fmt.Printf("DEBUG: ❌ Scroll failed - no scrollable container found\n")
		}
		return b
	}
	return false
}

func (m *ScrollAllTabMethod) extractVisibleImages(page playwright.Page) []BusinessImage {
	selectors := []string{
		`img[src*="googleusercontent.com"]`,
		`img[src*="gstatic.com"]`,
		`div[style*="googleusercontent.com"]`,    // Background images
		`img[data-src*="googleusercontent.com"]`, // Lazy loaded
		`a[data-photo-index] img`,                // Photo gallery links
		`button[data-photo-index] img`,           // Photo buttons
	}

	var images []BusinessImage
	urlsSeen := make(map[string]bool)

	for _, selector := range selectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}

		fmt.Printf("DEBUG: Selector '%s' found %d elements\n", selector, len(elements))

		for i, element := range elements {
			// Try multiple ways to get URL
			var url string

			// Method 1: src attribute
			if src, err := element.GetAttribute("src"); err == nil && src != "" {
				url = src
			}

			// Method 2: data-src attribute
			if url == "" {
				if dataSrc, err := element.GetAttribute("data-src"); err == nil && dataSrc != "" {
					url = dataSrc
				}
			}

			// Method 3: background-image from style
			if url == "" {
				if style, err := element.GetAttribute("style"); err == nil && style != "" {
					url = m.extractURLFromStyle(style)
				}
			}

			if url == "" || urlsSeen[url] {
				continue
			}

			if !m.isValidImageURL(url) {
				continue
			}

			urlsSeen[url] = true
			altText, _ := element.GetAttribute("alt")

			images = append(images, BusinessImage{
				URL:          m.enhanceImageURL(url),
				ThumbnailURL: m.createThumbnailURL(url),
				AltText:      altText,
				Category:     "all",
				Index:        i,
			})
		}
	}

	fmt.Printf("DEBUG: Extracted %d unique images from DOM\n", len(images))
	return images
}

func (m *ScrollAllTabMethod) extractURLFromStyle(style string) string {
	if start := strings.Index(style, "url("); start != -1 {
		start += 4
		if strings.HasPrefix(style[start:], `"`) || strings.HasPrefix(style[start:], `'`) {
			start++
		}

		if end := strings.IndexAny(style[start:], `"')`); end != -1 {
			url := style[start : start+end]
			// Clean up HTML entities
			url = strings.ReplaceAll(url, "&quot;", "")
			url = strings.ReplaceAll(url, "&amp;", "&")
			return url
		}
	}
	return ""
}

func (m *ScrollAllTabMethod) isValidImageURL(url string) bool {
	// Must contain Google image domains
	if !strings.Contains(url, "googleusercontent.com") && !strings.Contains(url, "gstatic.com") {
		return false
	}

	// Filter out junk images
	junkPatterns := []string{
		"loader",           // Loading spinners
		"spinner",          // Loading animations
		".gif",             // GIF files (usually UI elements/loaders)
		"icon",             // UI icons
		"logo",             // Logos
		"marker",           // Map markers
		"pin",              // Map pins
		"avatar",           // User avatars (too small)
		"=s32",             // 32px images (too small)
		"=s40",             // 40px images (too small)
		"=w35",             // 35px wide (too small)
		"=w40",             // 40px wide (too small)
		"=w50",             // 50px wide (too small)
		"tactile/basepage", // UI elements
		"basepage",         // Base page elements
	}

	for _, pattern := range junkPatterns {
		if strings.Contains(strings.ToLower(url), pattern) {
			return false
		}
	}

	// Must be reasonably sized (at least 100px)
	if strings.Contains(url, "=w") {
		// Extract width from URL like =w80-h60
		if parts := strings.Split(url, "=w"); len(parts) > 1 {
			widthStr := strings.Split(parts[1], "-")[0]
			width := 0
			for _, r := range widthStr {
				if r >= '0' && r <= '9' {
					width = width*10 + int(r-'0')
				} else {
					break
				}
			}
			if width > 0 && width < 100 {
				return false // Too small
			}
		}
	}

	return true
}

func (m *ScrollAllTabMethod) enhanceImageURL(url string) string {
	if strings.Contains(url, "googleusercontent.com") {
		if strings.Contains(url, "=w") {
			parts := strings.Split(url, "=w")
			if len(parts) > 1 {
				return parts[0] + "=w1920-h1080-k-no"
			}
		}
		return url + "=w1920-h1080-k-no"
	}
	return url
}

func (m *ScrollAllTabMethod) createThumbnailURL(url string) string {
	if strings.Contains(url, "googleusercontent.com") {
		if strings.Contains(url, "=w") {
			parts := strings.Split(url, "=w")
			if len(parts) > 1 {
				return parts[0] + "=w200-h200-c"
			}
		}
		return url + "=w200-h200-c"
	}
	return url
}

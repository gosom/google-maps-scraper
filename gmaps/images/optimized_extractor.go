package images

import (
	"context"
	"fmt"
	"log/slog"
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

		slog.Debug("trying_extraction_method", slog.Int("method_index", i+1), slog.String("method", method.Name()))

		// Create method-specific context with shorter timeout
		methodTimeout := time.Duration(float64(e.maxTimeout) * 0.4) // 40% of total time per method
		methodCtx, methodCancel := context.WithTimeout(extractCtx, methodTimeout)

		images, err := method.Extract(methodCtx, e.page)
		methodCancel()

		if err != nil {
			slog.Warn("extraction_method_failed", slog.String("method", method.Name()), slog.Any("error", err))
			lastError = err

			// CRITICAL: If ScrollAllTab method fails, still stop here - don't try TabBasedMethod
			if method.Name() == "ScrollAllTab" {
				slog.Debug("scroll_all_tab_failed_stopping", slog.String("reason", "avoid tab clicking"))
				break
			}
			continue
		}

		if len(images) > 0 {
			slog.Debug("extraction_method_succeeded", slog.String("method", method.Name()), slog.Int("count", len(images)))
			allImages = e.mergeImages(allImages, images)

			// CRITICAL: If ScrollAllTab succeeds with ANY images, stop immediately
			if method.Name() == "ScrollAllTab" && len(allImages) > 0 {
				slog.Debug("scroll_all_tab_sufficient_stopping", slog.Int("count", len(allImages)), slog.String("reason", "avoid tab clicking"))
				break
			}

			// If we have enough images, stop trying other methods
			if len(allImages) >= 20 {
				slog.Debug("sufficient_images_collected", slog.Int("count", len(allImages)))
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

	slog.Debug("optimized_extraction_completed", slog.Int("count", len(allImages)), slog.Int("duration_ms", metadata.LoadTime))
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

		slog.Debug("direct_gallery_elements_found", slog.Int("count", len(elements)), slog.String("selector", selector))

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

		slog.Debug("processing_tab", slog.Int("tab_index", i), slog.String("tab_name", tab.Name))

		tabImages, err := m.processTab(ctx, page, tab)
		if err != nil {
			slog.Warn("tab_processing_failed", slog.String("tab_name", tab.Name), slog.Any("error", err))
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

// ScrollAllTabMethod scrolls in "All" tab without clicking other tabs (FAST, no dialogs!)
type ScrollAllTabMethod struct{}

func (m *ScrollAllTabMethod) Name() string  { return "ScrollAllTab" }
func (m *ScrollAllTabMethod) Priority() int { return 110 } // Highest priority!

func (m *ScrollAllTabMethod) Extract(ctx context.Context, page playwright.Page) ([]BusinessImage, error) {
	slog.Debug("scroll_all_tab_extraction_starting")

	// Step 1: Try to navigate to images section
	if err := m.navigateToImages(page); err != nil {
		slog.Debug("photos_navigation_failed_using_current_page", slog.Any("error", err))
	}

	// Step 2: Try JavaScript-based extraction (most reliable in headless)
	// Retry up to 3 times with increasing waits if we get 0 images
	var jsImages []BusinessImage
	for attempt := 1; attempt <= 3; attempt++ {
		var err error
		jsImages, err = m.extractViaJavaScript(page)
		if err == nil && len(jsImages) > 3 {
			slog.Debug("js_extraction_succeeded", slog.Int("count", len(jsImages)), slog.Int("attempt", attempt))
			return jsImages, nil
		}
		if attempt < 3 {
			slog.Debug("js_extraction_insufficient_retrying", slog.Int("count", len(jsImages)), slog.Int("attempt", attempt))
			time.Sleep(2 * time.Second)
		}
	}
	slog.Debug("js_extraction_exhausted_trying_scroll", slog.Int("count", len(jsImages)), slog.Int("attempts", 3))

	// Step 3: Fall back to scroll and collect
	scrollImages, err := m.scrollAndCollectImages(ctx, page)
	if err != nil {
		return jsImages, err // Return whatever JS found
	}

	// Merge JS + scroll results
	urlSet := make(map[string]bool)
	var merged []BusinessImage
	for _, img := range jsImages {
		if !urlSet[img.URL] {
			urlSet[img.URL] = true
			merged = append(merged, img)
		}
	}
	for _, img := range scrollImages {
		if !urlSet[img.URL] {
			urlSet[img.URL] = true
			merged = append(merged, img)
		}
	}
	slog.Debug("images_merged", slog.Int("total", len(merged)), slog.Int("js_count", len(jsImages)), slog.Int("scroll_count", len(scrollImages)))
	return merged, nil
}

// extractViaJavaScript uses page.Evaluate to find ALL image URLs from the page
// This works in headless mode where DOM selectors fail
func (m *ScrollAllTabMethod) extractViaJavaScript(page playwright.Page) ([]BusinessImage, error) {
	result, err := page.Evaluate(`async () => {
		const urls = new Set();
		
		function collectImages() {
			// Background-image from inline style attribute (most reliable)
			document.querySelectorAll('[style*="googleusercontent"]').forEach(el => {
				const match = el.getAttribute('style').match(/url\(["']?(https:\/\/[^"')]+googleusercontent[^"')]+)["']?\)/);
				if (match) urls.add(match[1]);
			});
			
			// Background-image via computed styles (WHERE GOOGLE HIDES PHOTOS)
			// Narrowed selector to avoid iterating ALL DOM nodes and forcing layout/style recomputation
			document.querySelectorAll('div[style], span[style], img, [class*="photo"], [class*="image"], [class*="gallery"], [style*="background"]').forEach(el => {
				const computed = window.getComputedStyle(el);
				if (computed.backgroundImage && computed.backgroundImage !== 'none') {
					const match = computed.backgroundImage.match(/url\(["']?(https:\/\/[^"')]+googleusercontent[^"')]+)["']?\)/);
					if (match) urls.add(match[1]);
				}
			});
			
			// img src attributes
			document.querySelectorAll('img').forEach(img => {
				if (img.src && img.src.includes('googleusercontent.com')) urls.add(img.src);
				if (img.dataset && img.dataset.src && img.dataset.src.includes('googleusercontent.com')) urls.add(img.dataset.src);
			});
		}
		
		// Find scrollable photos container (retry if not loaded yet)
		let container = null;
		for (let retry = 0; retry < 3; retry++) {
			container = document.querySelector('.m6QErb.DxyBCb.kA9KIf.dS8AEf.XiKgde') 
				|| document.querySelector('.m6QErb.DxyBCb.XiKgde')
				|| document.querySelector('.m6QErb.DxyBCb');
			
			collectImages();
			if (container || urls.size > 0) break;
			// Gallery not loaded yet — wait and retry
			await new Promise(r => setTimeout(r, 2000));
		}
		
		if (container && container.scrollHeight > container.clientHeight) {
			// Scroll and collect incrementally
			const maxScrolls = 20;
			const scrollStep = 1500;
			let prevSize = 0;
			let stableCount = 0;
			
			for (let i = 0; i < maxScrolls; i++) {
				collectImages();
				container.scrollBy(0, scrollStep);
				await new Promise(r => setTimeout(r, 400)); // Wait for lazy load
				
				if (urls.size === prevSize) {
					stableCount++;
					if (stableCount >= 3) break; // No new images after 3 scrolls
				} else {
					stableCount = 0;
				}
				prevSize = urls.size;
			}
			
			// Final collection after last scroll
			collectImages();
		} else {
			// No scrollable container, just collect what's visible
			collectImages();
		}
		
		// Also extract from script tags
		document.querySelectorAll('script').forEach(script => {
			const text = script.textContent || '';
			const regex = /https:\/\/lh3\.googleusercontent\.com\/[^"'\s\\)]+/g;
			let match;
			while ((match = regex.exec(text)) !== null) {
				urls.add(match[0]);
			}
		});

		return Array.from(urls);
	}`)

	if err != nil {
		slog.Debug("js_extraction_error", slog.Any("error", err))
		return nil, err
	}

	urlList, ok := result.([]interface{})
	if !ok {
		slog.Debug("js_extraction_unexpected_type")
		return nil, fmt.Errorf("unexpected result type")
	}

	var images []BusinessImage
	for i, u := range urlList {
		url, ok := u.(string)
		if !ok || url == "" {
			continue
		}
		if !m.isValidImageURL(url) {
			continue
		}
		images = append(images, BusinessImage{
			URL:          m.enhanceImageURL(url),
			ThumbnailURL: m.createThumbnailURL(url),
			Category:     "all",
			Index:        i,
		})
	}

	slog.Debug("js_extraction_valid_images", slog.Int("count", len(images)))
	return images, nil
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
				slog.Debug("clicked_photos_button", slog.String("selector", selector))
				time.Sleep(3000 * time.Millisecond) // Wait 3s for Photos tab to load (proxy can be slow)

				// Debug: dump what's on the page after click
				url, _ := page.Evaluate(`() => window.location.href`)
				slog.Debug("current_url_after_photos_click", slog.Any("url", url))

				// Check what containers exist
				containerCheck, _ := page.Evaluate(`() => {
					const containers = [];
					document.querySelectorAll('[class*="m6QErb"]').forEach(el => {
						containers.push(el.className + ' scrollH=' + el.scrollHeight + ' clientH=' + el.clientHeight);
					});
					const imgCount = document.querySelectorAll('img[src*="googleusercontent"]').length;
					const bgCount = document.querySelectorAll('[style*="googleusercontent"]').length;
					const allImgs = document.querySelectorAll('img').length;
					return {containers, imgCount, bgCount, allImgs};
				}`)
				slog.Debug("page_state_after_photos_click", slog.Any("state", containerCheck))

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
	maxScrolls := 8 // Balanced: enough scrolls for most images without hanging
	maxStable := 2  // Exit after 2 consecutive scrolls with no new images
	startTime := time.Now()
	maxDuration := 15 * time.Second // 15s timeout - balanced speed vs coverage

	slog.Debug("scroll_starting", slog.Int("max_scrolls", maxScrolls), slog.Duration("timeout", maxDuration))

	// ALWAYS extract images from initial view first (before scrolling)
	slog.Debug("extracting_initial_view_images")
	initialImages := m.extractVisibleImages(page)
	slog.Debug("initial_view_images_found", slog.Int("count", len(initialImages)))
	for _, img := range initialImages {
		if !urlSet[img.URL] {
			urlSet[img.URL] = true
			allImages = append(allImages, img)
		}
	}

	for scrollCount < maxScrolls {
		// Hard timeout check
		if time.Since(startTime) > maxDuration {
			slog.Debug("scroll_hard_timeout", slog.Duration("timeout", maxDuration))
			break
		}

		select {
		case <-ctx.Done():
			slog.Debug("scroll_context_cancelled")
			return allImages, nil
		default:
		}

		previousCount := len(urlSet)

		// Fast scroll with logging
		slog.Debug("attempting_scroll", slog.Int("scroll_number", scrollCount+1), slog.Int("max_scrolls", maxScrolls))
		scrolled := m.scrollGallery(page)
		if !scrolled {
			slog.Debug("scroll_exhausted", slog.Int("attempts", scrollCount), slog.Int("count", len(allImages)))
			break
		}
		slog.Debug("scroll_successful", slog.Int("scroll_number", scrollCount+1))

		scrollCount++

		// Wait for lazy loading after scroll
		time.Sleep(500 * time.Millisecond)

		// Extract visible images after scroll
		slog.Debug("extracting_images_after_scroll", slog.Int("scroll_number", scrollCount))
		newImages := m.extractVisibleImages(page)
		slog.Debug("new_image_elements_found", slog.Int("count", len(newImages)))
		for _, img := range newImages {
			if !urlSet[img.URL] {
				urlSet[img.URL] = true
				allImages = append(allImages, img)
			}
		}
		slog.Debug("unique_images_so_far", slog.Int("count", len(allImages)))

		newFound := len(urlSet) - previousCount
		slog.Debug("scroll_progress", slog.Int("scroll_number", scrollCount), slog.Int("new_found", newFound), slog.Int("total", len(urlSet)))

		if newFound == 0 {
			stableCount++
			if stableCount >= maxStable {
				slog.Debug("no_new_images_stopping", slog.Int("stable_scrolls", maxStable))
				break
			}
		} else {
			stableCount = 0
		}
	}

	slog.Debug("scroll_all_tab_completed", slog.Int("count", len(allImages)), slog.Int("scrolls", scrollCount))
	return allImages, nil
}

func (m *ScrollAllTabMethod) scrollGallery(page playwright.Page) bool {
	scrolled, err := page.Evaluate(`() => {
		const selectors = [
			'div.m6QErb.DxyBCb.kA9KIf.dS8AEf.XiKgde', // Feb 2026: full photos container
			'div.m6QErb.DxyBCb.XiKgde',                 // Feb 2026: partial match
			'div.m6QErb.XiKgde',                         // Previous: nested photo container
			'div.EGN8xd',                                // Previous: photo grid container
			'div.m6QErb.DxyBCb',                         // Fallback: common container
			'div[role="main"]',                          // Fallback: main content
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
		slog.Debug("scroll_evaluation_error", slog.Any("error", err))
		return false
	}

	if b, ok := scrolled.(bool); ok {
		if b {
			slog.Debug("scroll_result_success")
		} else {
			slog.Debug("scroll_result_failed", slog.String("reason", "no scrollable container found"))
		}
		return b
	}
	return false
}

func (m *ScrollAllTabMethod) extractVisibleImages(page playwright.Page) []BusinessImage {
	var images []BusinessImage
	urlsSeen := make(map[string]bool)

	// PRIMARY METHOD: Background images (Google uses background-image CSS, not <img> tags!)
	// This is where 95% of gallery photos are stored
	bgSelectors := []string{
		`div[style*="googleusercontent.com"]`,               // Main: background-image with googleusercontent
		`div[style*="background-image"][style*="lh3.goog"]`, // Alternative match
	}

	for _, selector := range bgSelectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}
		slog.Debug("bg_selector_elements_found", slog.String("selector", selector), slog.Int("count", len(elements)))

		for i, element := range elements {
			style, err := element.GetAttribute("style")
			if err != nil || style == "" {
				continue
			}
			url := m.extractURLFromStyle(style)
			if url == "" || urlsSeen[url] {
				continue
			}
			if !m.isValidImageURL(url) {
				continue
			}
			urlsSeen[url] = true
			images = append(images, BusinessImage{
				URL:          m.enhanceImageURL(url),
				ThumbnailURL: m.createThumbnailURL(url),
				Category:     "all",
				Index:        i,
			})
		}
	}

	slog.Debug("bg_style_images_found", slog.Int("count", len(images)))

	// SECONDARY METHOD: <img> tags (for thumbnails, category previews)
	imgSelectors := []string{
		`img.kSOdnb.Lyrzac[src*="googleusercontent.com"]`, // Feb 2026: main place photos
		`img.QUPxxe[src*="googleusercontent.com"]`,        // Feb 2026: additional photos
		`img[src*="googleusercontent.com"]`,               // Generic googleusercontent
	}

	for _, selector := range imgSelectors {
		elements, err := page.Locator(selector).All()
		if err != nil {
			continue
		}
		slog.Debug("img_selector_elements_found", slog.String("selector", selector), slog.Int("count", len(elements)))

		for i, element := range elements {
			src, err := element.GetAttribute("src")
			if err != nil || src == "" {
				continue
			}
			if urlsSeen[src] {
				continue
			}
			if !m.isValidImageURL(src) {
				continue
			}
			urlsSeen[src] = true
			altText, _ := element.GetAttribute("alt")
			images = append(images, BusinessImage{
				URL:          m.enhanceImageURL(src),
				ThumbnailURL: m.createThumbnailURL(src),
				AltText:      altText,
				Category:     "all",
				Index:        i,
			})
		}
	}

	slog.Debug("total_unique_images_from_dom", slog.Int("count", len(images)))
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

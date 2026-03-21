package images

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/playwright-community/playwright-go"
)

// ImageProcessor handles concurrent processing with performance optimizations
type ImageProcessor struct {
	rateLimiter *AdaptiveRateLimiter
	maxRetries  int
	memoryPool  *ImageBufferPool
}

// AdaptiveRateLimiter implements intelligent rate limiting to avoid detection
type AdaptiveRateLimiter struct {
	baseDelay    time.Duration
	backoffMult  float64
	successCount int64
	failCount    int64
	mu           sync.RWMutex
}

// ImageBufferPool manages memory pooling for BusinessImage slices
type ImageBufferPool struct {
	pool sync.Pool
}

// ScrapeResult represents the result of a scraping operation with error handling
type ScrapeResult struct {
	Images      []BusinessImage   `json:"images"`
	Metadata    *ScrapingMetadata `json:"metadata"`
	ImageCount  int               `json:"image_count"`
	Errors      []string          `json:"errors,omitempty"`
	PartialData bool              `json:"partial_data"`
}

// NewImageProcessor creates a new image processor with performance optimizations
func NewImageProcessor(maxRetries int) *ImageProcessor {
	return &ImageProcessor{
		rateLimiter: NewAdaptiveRateLimiter(15 * time.Second), // Conservative base delay
		maxRetries:  maxRetries,
		memoryPool:  NewImageBufferPool(),
	}
}

// NewAdaptiveRateLimiter creates a new adaptive rate limiter
func NewAdaptiveRateLimiter(baseDelay time.Duration) *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		baseDelay:   baseDelay,
		backoffMult: 1.5,
	}
}

// NewImageBufferPool creates a new memory pool for image buffers
func NewImageBufferPool() *ImageBufferPool {
	return &ImageBufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				// Pre-allocate capacity for 50 images to reduce allocations
				return make([]BusinessImage, 0, 50)
			},
		},
	}
}

// ProcessBusiness extracts images from a business page with retry logic
func (ip *ImageProcessor) ProcessBusiness(ctx context.Context, page playwright.Page) (*ScrapeResult, error) {
	return ip.processWithRetry(ctx, page, 0)
}

// processWithRetry implements retry logic with exponential backoff
func (ip *ImageProcessor) processWithRetry(ctx context.Context, page playwright.Page, attempt int) (*ScrapeResult, error) {
	if attempt >= ip.maxRetries {
		return &ScrapeResult{
			Errors:      []string{fmt.Sprintf("max retries (%d) exceeded", ip.maxRetries)},
			PartialData: true,
		}, fmt.Errorf("max retries exceeded")
	}

	// Apply adaptive rate limiting
	ip.rateLimiter.Wait()

	result := &ScrapeResult{
		Images: ip.memoryPool.Get(),
		Errors: make([]string, 0),
	}

	// Create extractor with optimized settings
	extractor := NewImageExtractor(page)

	// Extract images with timeout
	extractCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	images, err := extractor.ExtractAllImages(extractCtx)
	if err != nil {
		ip.rateLimiter.RecordFailure()

		// For certain errors, retry immediately
		if shouldRetryImmediately(err) {
			// Return pooled buffer before retrying since we won't use it
			ip.memoryPool.Put(result.Images)
			return ip.processWithRetry(ctx, page, attempt+1)
		}

		// For other errors, return partial results if available
		result.Errors = append(result.Errors, err.Error())
		result.PartialData = true

		if len(images) > 0 {
			result.Images = append(result.Images, images...)
		}

		// Copy images before returning pooled buffer
		if len(result.Images) > 0 {
			imagesCopy := make([]BusinessImage, len(result.Images))
			copy(imagesCopy, result.Images)
			ip.memoryPool.Put(result.Images)
			result.Images = imagesCopy
		} else {
			ip.memoryPool.Put(result.Images)
			result.Images = nil
		}

		return result, nil // Don't return error to allow partial processing
	}

	ip.rateLimiter.RecordSuccess()

	// Copy images to result buffer
	result.Images = append(result.Images, images...)
	result.ImageCount = len(images)
	result.Metadata = extractor.GetMetadata()

	// Create a copy to return, then return original buffer to pool
	resultCopy := make([]BusinessImage, len(result.Images))
	copy(resultCopy, result.Images)
	ip.memoryPool.Put(result.Images) // safe: we copied

	return &ScrapeResult{
		Images:     resultCopy,
		Metadata:   result.Metadata,
		ImageCount: result.ImageCount,
		Errors:     result.Errors,
	}, nil
}

// Wait implements adaptive rate limiting with jitter
func (r *AdaptiveRateLimiter) Wait() {
	r.mu.RLock()
	delay := r.calculateDelay()
	r.mu.RUnlock()

	// Add jitter to avoid detection patterns (±25% of delay)
	jitterRange := float64(delay) * 0.25
	jitter := time.Duration(rand.Float64()*jitterRange*2 - jitterRange)
	finalDelay := delay + jitter

	// Ensure minimum delay is always positive
	if finalDelay < time.Second {
		finalDelay = time.Second
	}

	time.Sleep(finalDelay)
}

// calculateDelay computes the current delay based on success/failure ratio
func (r *AdaptiveRateLimiter) calculateDelay() time.Duration {
	successCount := atomic.LoadInt64(&r.successCount)
	failCount := atomic.LoadInt64(&r.failCount)

	// If we have a high success rate, gradually decrease delay
	if successCount > 10 && failCount == 0 {
		return time.Duration(float64(r.baseDelay) * 0.8)
	}

	// If we have failures, increase delay
	if failCount > 0 {
		multiplier := 1.0 + (float64(failCount) * 0.5)
		return time.Duration(float64(r.baseDelay) * multiplier)
	}

	return r.baseDelay
}

// RecordSuccess records a successful operation
func (r *AdaptiveRateLimiter) RecordSuccess() {
	atomic.AddInt64(&r.successCount, 1)

	// Reset failure count after sustained success
	if atomic.LoadInt64(&r.successCount)%20 == 0 {
		atomic.StoreInt64(&r.failCount, 0)
	}
}

// RecordFailure records a failed operation
func (r *AdaptiveRateLimiter) RecordFailure() {
	atomic.AddInt64(&r.failCount, 1)

	// Reset success count after failures to be more conservative
	if atomic.LoadInt64(&r.failCount) > 3 {
		atomic.StoreInt64(&r.successCount, 0)
	}
}

// Get retrieves a buffer from the pool
func (pool *ImageBufferPool) Get() []BusinessImage {
	return pool.pool.Get().([]BusinessImage)
}

// Put returns a buffer to the pool after resetting it
func (pool *ImageBufferPool) Put(images []BusinessImage) {
	// Reset slice but keep capacity
	images = images[:0]
	pool.pool.Put(images)
}

// shouldRetryImmediately determines if an error warrants immediate retry
func shouldRetryImmediately(err error) bool {
	errStr := err.Error()

	// Network-related errors that might be transient
	transientErrors := []string{
		"timeout",
		"connection reset",
		"connection refused",
		"network is unreachable",
		"temporary failure",
	}

	for _, transient := range transientErrors {
		if contains(errStr, transient) {
			return true
		}
	}

	return false
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// HybridImageExtractor combines APP_INITIALIZATION_STATE with DOM extraction
type HybridImageExtractor struct {
	page        playwright.Page
	processor   *ImageProcessor
	fallbackDOM bool
}

// NewHybridImageExtractor creates an extractor that tries both methods
func NewHybridImageExtractor(page playwright.Page) *HybridImageExtractor {
	return &HybridImageExtractor{
		page:        page,
		processor:   NewImageProcessor(3),
		fallbackDOM: true,
	}
}

// ExtractImagesHybrid performs DOM extraction for business images
func (h *HybridImageExtractor) ExtractImagesHybrid(ctx context.Context) (*ScrapeResult, error) {
	result := &ScrapeResult{
		Images: make([]BusinessImage, 0),
		Errors: make([]string, 0),
	}

	// Try DOM extraction
	if h.fallbackDOM {
		domResult, err := h.processor.ProcessBusiness(ctx, h.page)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("DOM extraction failed: %v", err))
			result.PartialData = true
		} else {
			// Merge results, avoiding duplicates
			existingURLs := make(map[string]bool)
			for _, img := range result.Images {
				existingURLs[img.URL] = true
			}

			for _, img := range domResult.Images {
				if !existingURLs[img.URL] {
					result.Images = append(result.Images, img)
				}
			}

			result.ImageCount = len(result.Images)
			result.Metadata = domResult.Metadata

			if len(domResult.Errors) > 0 {
				result.Errors = append(result.Errors, domResult.Errors...)
			}
		}
	}

	return result, nil
}


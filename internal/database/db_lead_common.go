package database

const (
	batchSize = 500
)

// BreakIntoBatches splits a slice of any type into smaller batches of the specified size.
// Type parameter T can be any type (uint64, string, custom structs, etc.)
func BreakIntoBatches[T any](items []T, batchSize int) [][]T {
    if batchSize <= 0 {
        batchSize = 1 // Ensure minimum batch size of 1
    }

    numBatches := (len(items) + batchSize - 1) / batchSize
    batches := make([][]T, 0, numBatches)

    for i := 0; i < len(items); i += batchSize {
        end := i + batchSize
        if end > len(items) {
            end = len(items)
        }
        batches = append(batches, items[i:end])
    }

    return batches
} 
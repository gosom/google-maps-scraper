package scrapemate

// Result is the struct items of which the Results channel has
type Result struct {
	Job  IJob
	Data any
}

// CsvCapable is an interface for types that can be converted to csv
// It is used to convert the Data of a Result to csv
type CsvCapable interface {
	CsvHeaders() []string
	CsvRow() []string
}

package webrunner

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"sync"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
)

// DualWriter scrive sia in CSV che in JSON
type DualWriter struct {
	csvWriter  scrapemate.ResultWriter
	jsonWriter *JSONWriter
}

// NewDualWriter crea un writer che scrive sia CSV che JSON
func NewDualWriter(csvW io.Writer, jsonW io.Writer) *DualWriter {
	return &DualWriter{
		csvWriter:  csvwriter.NewCsvWriter(csv.NewWriter(csvW)),
		jsonWriter: NewJSONWriter(jsonW),
	}
}

// Run implementa l'interfaccia ResultWriter
func (d *DualWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	// Creiamo due canali per dividere i risultati
	csvChan := make(chan scrapemate.Result, 100)
	jsonChan := make(chan scrapemate.Result, 100)

	// WaitGroup per aspettare che entrambi i writer finiscano
	var wg sync.WaitGroup
	wg.Add(2)

	// Variabili per catturare eventuali errori
	var csvErr, jsonErr error

	// Avvia il CSV writer
	go func() {
		defer wg.Done()
		csvErr = d.csvWriter.Run(ctx, csvChan)
	}()

	// Avvia il JSON writer
	go func() {
		defer wg.Done()
		jsonErr = d.jsonWriter.Run(ctx, jsonChan)
	}()

	// Distribuisci i risultati a entrambi i writer
	go func() {
		defer close(csvChan)
		defer close(jsonChan)

		for {
			select {
			case <-ctx.Done():
				return
			case result, ok := <-in:
				if !ok {
					return
				}

				// Invia a entrambi i canali
				select {
				case csvChan <- result:
				case <-ctx.Done():
					return
				}

				select {
				case jsonChan <- result:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Aspetta che entrambi i writer finiscano
	wg.Wait()

	// Restituisci il primo errore se presente
	if csvErr != nil {
		return csvErr
	}
	return jsonErr
}

// JSONWriter implementa un writer per JSON
type JSONWriter struct {
	mu      sync.Mutex
	results []interface{}
	writer  io.Writer
	closed  chan struct{}
}

// NewJSONWriter crea un nuovo JSONWriter
func NewJSONWriter(w io.Writer) *JSONWriter {
	return &JSONWriter{
		results: make([]interface{}, 0),
		writer:  w,
		closed:  make(chan struct{}),
	}
}

// Run implementa l'interfaccia ResultWriter
func (j *JSONWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	defer close(j.closed)

	for {
		select {
		case <-ctx.Done():
			return j.Flush()
		case result, ok := <-in:
			if !ok {
				return j.Flush()
			}

			j.mu.Lock()
			j.results = append(j.results, result.Data)
			j.mu.Unlock()
		}
	}
}

// Flush scrive tutti i risultati nel writer come array JSON
func (j *JSONWriter) Flush() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	encoder := json.NewEncoder(j.writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(j.results)
}

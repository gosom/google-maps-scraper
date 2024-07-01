package models

type JsonInput struct {
	Polygons   [][][]float64 `json:"polygons"`
	Keyword    []string      `json:"keywords"`
	Resolution int           `json:"resolution"`
	ZoomLevel  int           `json:"zoom_level"`
}
